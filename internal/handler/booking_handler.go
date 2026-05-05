package handler

import (
	"booking_api/internal/apperror"
	"booking_api/internal/dto"
	"booking_api/internal/services"
	"errors"
	"math"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type BookingHandler struct {
	service services.BookingService
	logger  *zap.Logger
}

func NewBookingHandler(service services.BookingService, logger *zap.Logger) *BookingHandler {
	return &BookingHandler{
		service: service,
		logger:  logger,
	}
}

func (h *BookingHandler) RegisterRoutes(rg *gin.RouterGroup) {
	bookings := rg.Group("/bookings")
	{
		bookings.POST("/hold", h.HoldSeats)
		bookings.GET("/:id", h.GetBooking)
		bookings.POST("/:id/confirm", h.ConfirmBooking)
		bookings.POST("/:id/cancel", h.CancelBooking)
		bookings.GET("/user/:user_id", h.GetBookingsByUser)
		bookings.POST("/release-expired", h.ReleaseExpiredHolds)
	}

	showtimes := rg.Group("/showtimes")
	{
		showtimes.GET("/:showtime_id/seats", h.GetSeatsStatus)
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

	booking, err := h.service.HoldSeats(req)
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusCreated, dto.SuccessResponse{
		Message: "seats held successfully",
		Data:    booking,
	})
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

	booking, err := h.service.ConfirmBooking(bookingID)
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse{
		Message: "booking confirmed successfully",
		Data:    booking,
	})
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

	if err := h.service.CancelBooking(bookingID); err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto.SuccessResponse{
		Message: "booking canceled successfully",
	})
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

	booking, err := h.service.GetBooking(bookingID)
	if err != nil {
		h.handleError(c, err)
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
