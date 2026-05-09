package handler

import (
	"booking_api/internal/apperror"
	"booking_api/internal/dto"
	"booking_api/internal/middleware"
	"booking_api/internal/realtime"
	"booking_api/internal/services"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type BookingHandler struct {
	service services.BookingService
	logger  *zap.Logger
	seatHub *realtime.SeatHub

	releaserStop chan struct{}
	releaserOnce sync.Once
}

func NewBookingHandler(service services.BookingService, logger *zap.Logger) *BookingHandler {
	return &BookingHandler{
		service: service,
		logger:  logger,
		seatHub: realtime.NewSeatHub(),

		releaserStop: make(chan struct{}),
	}
}

func (h *BookingHandler) StartExpiredHoldReleaser(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				count, err := h.service.ReleaseExpiredHolds()
				if err != nil {
					h.logger.Warn("failed to auto-release expired holds", zap.Error(err))
					continue
				}
				if count == 0 {
					continue
				}
				for _, showtimeID := range h.seatHub.ShowtimeIDs() {
					h.broadcastSeats(showtimeID)
				}
			case <-h.releaserStop:
				return
			}
		}
	}()
}

func (h *BookingHandler) StopExpiredHoldReleaser() {
	h.releaserOnce.Do(func() {
		close(h.releaserStop)
	})
}

func (h *BookingHandler) RegisterRoutes(rg *gin.RouterGroup) {
	bookings := rg.Group("/bookings")
	{
		bookings.POST("/hold", h.HoldSeats)
		bookings.GET("/:id", h.GetBooking)
		bookings.POST("/:id/confirm", h.ConfirmBooking)
		bookings.POST("/:id/cancel", h.CancelBooking)
		bookings.GET("/user/:user_id", h.GetBookingsByUser)
		bookings.POST("/release-expired", middleware.RequireAdmin(), h.ReleaseExpiredHolds)
	}

	showtimes := rg.Group("/showtimes")
	{
		showtimes.GET("/:showtime_id/seats", h.GetSeatsStatus)
		showtimes.GET("/:showtime_id/seats/ws", h.StreamSeatsStatus)
	}

	admin := rg.Group("/admin", middleware.RequireAdmin())
	{
		admin.GET("/dashboard", h.GetAdminDashboard)
	}
}

// HoldSeats godoc
// @Summary Hold seats
// @Description Create a holding booking for selected seats.
// @Tags bookings
// @Accept json
// @Produce json
// @Param request body dto.HoldSeatsRequest true "Hold seats request"
// @Success 201 {object} dto.SuccessResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 409 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /bookings/hold [post]
func (h *BookingHandler) HoldSeats(c *gin.Context) {
	var req dto.HoldSeatsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid request: " + err.Error(),
		})
		return
	}
	authUserID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{
			Code:    http.StatusUnauthorized,
			Message: "authenticated user was not found in request context",
		})
		return
	}
	req.UserID = authUserID

	booking, err := h.service.HoldSeats(req)
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusCreated, dto.SuccessResponse{
		Message: "seats held successfully",
		Data:    booking,
	})
	h.broadcastSeats(req.ShowtimeID)
}

// ConfirmBooking godoc
// @Summary Confirm booking
// @Description Confirm a held booking and mark seats sold.
// @Tags bookings
// @Accept json
// @Produce json
// @Param id path string true "Booking ID"
// @Success 200 {object} dto.SuccessResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 404 {object} dto.ErrorResponse
// @Failure 409 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /bookings/{id}/confirm [post]
func (h *BookingHandler) ConfirmBooking(c *gin.Context) {
	bookingID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid booking ID",
		})
		return
	}

	authUserID, isAuthed := middleware.GetUserID(c)
	if !isAuthed {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{
			Code:    http.StatusUnauthorized,
			Message: "authenticated user was not found in request context",
		})
		return
	}

	current, err := h.service.GetBooking(bookingID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	if current.UserID != authUserID.String() && middleware.GetRole(c) != "ADMIN" {
		c.JSON(http.StatusForbidden, dto.ErrorResponse{
			Code:    http.StatusForbidden,
			Message: "you do not have permission to access this booking",
		})
		return
	}

	booking, err := h.service.ConfirmBooking(bookingID)
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse{
		Message: "booking confirmed successfully",
		Data:    booking,
	})

	if showtimeID, parseErr := uuid.Parse(booking.ShowTimeID); parseErr == nil {
		h.broadcastSeats(showtimeID)
	}
}

// CancelBooking godoc
// @Summary Cancel booking
// @Description Cancel booking and release held seats.
// @Tags bookings
// @Accept json
// @Produce json
// @Param id path string true "Booking ID"
// @Success 200 {object} dto.SuccessResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 404 {object} dto.ErrorResponse
// @Failure 409 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /bookings/{id}/cancel [post]
func (h *BookingHandler) CancelBooking(c *gin.Context) {
	bookingID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid booking ID",
		})
		return
	}

	authUserID, isAuthed := middleware.GetUserID(c)
	if !isAuthed {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{
			Code:    http.StatusUnauthorized,
			Message: "authenticated user was not found in request context",
		})
		return
	}

	booking, err := h.service.GetBooking(bookingID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	if booking.UserID != authUserID.String() && middleware.GetRole(c) != "ADMIN" {
		c.JSON(http.StatusForbidden, dto.ErrorResponse{
			Code:    http.StatusForbidden,
			Message: "you do not have permission to access this booking",
		})
		return
	}

	if err := h.service.CancelBooking(bookingID); err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse{
		Message: "booking canceled successfully",
	})

	if showtimeID, parseErr := uuid.Parse(booking.ShowTimeID); parseErr == nil {
		h.broadcastSeats(showtimeID)
	}
}

// GetBooking godoc
// @Summary Get booking by ID
// @Description Retrieve booking details by booking ID.
// @Tags bookings
// @Produce json
// @Param id path string true "Booking ID"
// @Success 200 {object} dto.SuccessResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 404 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /bookings/{id} [get]
func (h *BookingHandler) GetBooking(c *gin.Context) {
	bookingID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid booking ID",
		})
		return
	}

	authUserID, isAuthed := middleware.GetUserID(c)
	if !isAuthed {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{
			Code:    http.StatusUnauthorized,
			Message: "authenticated user was not found in request context",
		})
		return
	}

	booking, err := h.service.GetBooking(bookingID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	if booking.UserID != authUserID.String() && middleware.GetRole(c) != "ADMIN" {
		c.JSON(http.StatusForbidden, dto.ErrorResponse{
			Code:    http.StatusForbidden,
			Message: "you do not have permission to access this booking",
		})
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse{
		Data: booking,
	})
}

// GetBookingsByUser godoc
// @Summary Get bookings by user
// @Description Retrieve paginated bookings for a user.
// @Tags bookings
// @Produce json
// @Param user_id path string true "User ID"
// @Param page query int false "Page" default(1)
// @Param page_size query int false "Page size" default(20)
// @Success 200 {object} dto.PaginatedResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /bookings/user/{user_id} [get]
func (h *BookingHandler) GetBookingsByUser(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid user ID",
		})
		return
	}
	authUserID, isAuthed := middleware.GetUserID(c)
	if !isAuthed {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{
			Code:    http.StatusUnauthorized,
			Message: "authenticated user was not found in request context",
		})
		return
	}
	if userID != authUserID && middleware.GetRole(c) != "ADMIN" {
		c.JSON(http.StatusForbidden, dto.ErrorResponse{
			Code:    http.StatusForbidden,
			Message: "you do not have permission to access this user's bookings",
		})
		return
	}

	var pagination dto.PaginationQuery
	if err := c.ShouldBindQuery(&pagination); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid pagination params: " + err.Error(),
		})
		return
	}

	bookings, total, err := h.service.GetBookingsByUser(userID, pagination.Page, pagination.PageSize)
	if err != nil {
		h.handleError(c, err)
		return
	}

	totalPages := int(math.Ceil(float64(total) / float64(pagination.PageSize)))

	c.JSON(http.StatusOK, dto.PaginatedResponse{
		Data:       bookings,
		Page:       pagination.Page,
		PageSize:   pagination.PageSize,
		TotalItems: total,
		TotalPages: totalPages,
	})
}

// GetSeatsStatus godoc
// @Summary Get seats status
// @Description Get seats status for a showtime.
// @Tags showtimes
// @Produce json
// @Param showtime_id path string true "Showtime ID"
// @Success 200 {object} dto.SuccessResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /showtimes/{showtime_id}/seats [get]
func (h *BookingHandler) GetSeatsStatus(c *gin.Context) {
	showtimeID, err := uuid.Parse(c.Param("showtime_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid showtime ID",
		})
		return
	}

	seats, err := h.service.GetSeatsStatus(showtimeID)
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse{
		Data: seats,
	})
}

var seatStatusUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// StreamSeatsStatus godoc
// @Summary Stream seats status
// @Description Stream realtime seat status updates for a showtime over WebSocket.
// @Tags showtimes
// @Param showtime_id path string true "Showtime ID"
// @Router /showtimes/{showtime_id}/seats/ws [get]
func (h *BookingHandler) StreamSeatsStatus(c *gin.Context) {
	showtimeID, err := uuid.Parse(c.Param("showtime_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "invalid showtime ID",
		})
		return
	}

	conn, err := seatStatusUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Warn("failed to upgrade seat websocket", zap.Error(err))
		return
	}
	defer conn.Close()

	updates := h.seatHub.Subscribe(showtimeID)
	defer h.seatHub.Unsubscribe(showtimeID, updates)

	if payload, err := h.buildSeatsPayload(showtimeID); err == nil {
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			return
		}
	} else {
		h.logger.Warn("failed to build initial seat websocket payload", zap.Error(err))
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-done:
			return
		case payload := <-updates:
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-pingTicker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ReleaseExpiredHolds godoc
// @Summary Release expired holds
// @Description Release all expired holding bookings and seats.
// @Tags bookings
// @Accept json
// @Produce json
// @Success 200 {object} dto.SuccessResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /bookings/release-expired [post]
func (h *BookingHandler) ReleaseExpiredHolds(c *gin.Context) {
	count, err := h.service.ReleaseExpiredHolds()
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse{
		Message: "expired holds released",
		Data:    map[string]int64{"released_count": count},
	})

	for _, showtimeID := range h.seatHub.ShowtimeIDs() {
		h.broadcastSeats(showtimeID)
	}
}

// GetAdminDashboard godoc
// @Summary Get admin dashboard stats
// @Description Get aggregated booking, seat, and revenue statistics for the admin dashboard.
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} dto.SuccessResponse
// @Failure 401 {object} dto.ErrorResponse
// @Failure 403 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /admin/dashboard [get]
func (h *BookingHandler) GetAdminDashboard(c *gin.Context) {
	stats, err := h.service.GetDashboardStats()
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse{
		Data: stats,
	})
}

func (h *BookingHandler) broadcastSeats(showtimeID uuid.UUID) {
	payload, err := h.buildSeatsPayload(showtimeID)
	if err != nil {
		h.logger.Warn("failed to build seat status broadcast", zap.Error(err), zap.String("showtime_id", showtimeID.String()))
		return
	}
	h.seatHub.Broadcast(showtimeID, payload)
}

func (h *BookingHandler) buildSeatsPayload(showtimeID uuid.UUID) ([]byte, error) {
	status, err := h.service.GetSeatsStatus(showtimeID)
	if err != nil {
		return nil, err
	}

	return json.Marshal(gin.H{
		"type": "seat_status",
		"data": status,
	})
}

// ---------- Error Handler ----------

func (h *BookingHandler) handleError(c *gin.Context, err error) {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		c.JSON(appErr.Code, dto.ErrorResponse{
			Code:    appErr.Code,
			Message: appErr.Message,
		})
		return
	}

	h.logger.Error("unhandled error", zap.Error(err))
	c.JSON(http.StatusInternalServerError, dto.ErrorResponse{
		Code:    http.StatusInternalServerError,
		Message: "internal server error",
	})
}
