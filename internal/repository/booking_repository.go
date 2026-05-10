package repository

import (
	"booking_api/internal/apperror"
	"booking_api/internal/dto"
	"booking_api/internal/models"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BookingRepository interface {
	HoldSeats(req dto.HoldSeatsRequest) (*models.Booking, error)
	ConfirmBooking(bookingID uuid.UUID) (*models.Booking, error)
	CancelBooking(bookingID uuid.UUID) (*models.Booking, error)
	GetBookingByID(bookingID uuid.UUID) (*models.Booking, error)
	GetBookingsByUser(userID uuid.UUID, page, pageSize int) ([]models.Booking, int64, error)
	GetSeatsStatus(showtimeID uuid.UUID) ([]models.ShowTimeSeat, error)
	ReleaseExpiredHolds() (int64, error)
	GetDashboardStats() (*dto.DashboardStatsResponse, error)
	GetShowtimeQueueSettings(showtimeID uuid.UUID) (enabled bool, limit int, err error)
}

type bookingRepository struct {
	db     *gorm.DB
	logger *zap.Logger
}

func NewBookingRepository(db *gorm.DB, logger *zap.Logger) BookingRepository {
	return &bookingRepository{
		db:     db,
		logger: logger,
	}
}

func (r *bookingRepository) GetShowtimeQueueSettings(showtimeID uuid.UUID) (bool, int, error) {
	var row struct {
		QueueEnabled bool
		QueueLimit   int
	}
	if err := r.db.Model(&models.ShowTime{}).
		Select("queue_enabled", "queue_limit").
		Where("id = ?", showtimeID).
		Take(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, 0, apperror.NewNotFound("showtime not found")
		}
		return false, 0, apperror.NewInternal("failed to load showtime queue settings", err)
	}
	return row.QueueEnabled, row.QueueLimit, nil
}

func (r *bookingRepository) GetSeatsStatus(showtimeID uuid.UUID) ([]models.ShowTimeSeat, error) {
	var seats []models.ShowTimeSeat

	now := time.Now()

	err := r.db.
		Preload("Seat").
		Where("show_time_id = ?", showtimeID).
		Find(&seats).Error

	if err != nil {
		return nil, apperror.NewInternal("failed to get seats status", err)
	}

	if len(seats) > 0 {
		seatIDs := make([]uuid.UUID, 0, len(seats))
		for _, seat := range seats {
			seatIDs = append(seatIDs, seat.SeatID)
		}

		var prices []models.SeatPricing
		if err := r.db.
			Where("show_time_id = ? AND seat_id IN ?", showtimeID, seatIDs).
			Find(&prices).Error; err != nil {
			return nil, apperror.NewInternal("failed to get seat pricing", err)
		}

		priceMap := make(map[uuid.UUID]decimal.Decimal, len(prices))
		for _, p := range prices {
			priceMap[p.SeatID] = p.Price
		}

		for i := range seats {
			if price, ok := priceMap[seats[i].SeatID]; ok {
				priceCopy := price
				seats[i].Price = &priceCopy
			}
		}
	}

	for i := range seats {
		if seats[i].Status == models.ShowTimeSeatStatusHolding &&
			seats[i].ExpiresAt != nil &&
			seats[i].ExpiresAt.Before(now) {

			seats[i].Status = models.ShowTimeSeatStatusAvailable
			seats[i].BookingID = nil
			seats[i].ExpiresAt = nil
		}
	}

	return seats, nil
}

// ---------- HoldSeats ----------

func (r *bookingRepository) HoldSeats(req dto.HoldSeatsRequest) (*models.Booking, error) {
	var booking *models.Booking

	err := r.db.Transaction(func(tx *gorm.DB) error {
		var seats []models.ShowTimeSeat

		now := time.Now()
		expiresAt := now.Add(10 * time.Minute)

		if err := tx.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(`
				show_time_id = ? 
				AND seat_id IN ?
				AND (
					status = ? 
					OR (status = ? AND expires_at < ?)
				)
			`,
				req.ShowtimeID,
				req.SeatIDs,
				models.ShowTimeSeatStatusAvailable,
				models.ShowTimeSeatStatusHolding,
				now,
			).
			Order("seat_id").
			Find(&seats).Error; err != nil {
			return err
		}

		if len(seats) != len(req.SeatIDs) {
			var conflicted []models.ShowTimeSeat
			if err := tx.
				Preload("Seat").
				Where("show_time_id = ? AND seat_id IN ?", req.ShowtimeID, req.SeatIDs).
				Where(`
					NOT (
						status = ?
						OR (status = ? AND expires_at < ?)
					)
				`, models.ShowTimeSeatStatusAvailable, models.ShowTimeSeatStatusHolding, now).
				Order("seat_id").
				Find(&conflicted).Error; err != nil {
				return apperror.NewConflict("some seats are not available")
			}

			if len(conflicted) == 0 {
				return apperror.NewConflict("some seats are not available")
			}

			conflictedLabels := make([]string, 0, len(conflicted))
			for _, seat := range conflicted {
				if seat.Seat.Row == "" {
					conflictedLabels = append(conflictedLabels, seat.SeatID.String())
					continue
				}
				conflictedLabels = append(conflictedLabels, fmt.Sprintf("%s%d", seat.Seat.Row, seat.Seat.Number))
			}

			return apperror.NewConflict(
				fmt.Sprintf("seat(s) %s were already held by another buyer", strings.Join(conflictedLabels, ", ")),
			)
		}

		booking = &models.Booking{
			UserID:     req.UserID,
			ShowTimeID: req.ShowtimeID,
			Status:     models.BookingStatusHolding,
			ExpiresAt:  &expiresAt,
		}

		if err := tx.Create(booking).Error; err != nil {
			return fmt.Errorf("failed to create booking: %v", err)
		}

		var seatIDs []uuid.UUID
		for _, s := range seats {
			seatIDs = append(seatIDs, s.ID)
		}

		if err := tx.Model(&models.ShowTimeSeat{}).
			Where("id IN ?", seatIDs).
			Updates(map[string]interface{}{
				"status":     models.ShowTimeSeatStatusHolding,
				"booking_id": booking.ID,
				"expires_at": expiresAt,
			}).Error; err != nil {
			return fmt.Errorf("failed to update seats: %v", err)
		}

		var pricings []models.SeatPricing
		if err := tx.
			Where("show_time_id = ? AND seat_id IN ?", req.ShowtimeID, req.SeatIDs).
			Find(&pricings).Error; err != nil {
			return err
		}

		priceMap := make(map[uuid.UUID]decimal.Decimal)
		for _, p := range pricings {
			priceMap[p.SeatID] = p.Price
		}

		var items []models.BookingItem
		for _, s := range seats {
			price, ok := priceMap[s.SeatID]
			if !ok {
				return apperror.NewInternal(
					fmt.Sprintf("price not found for seat %s", s.SeatID), nil,
				)
			}

			items = append(items, models.BookingItem{
				BookingID:      booking.ID,
				ShowTimeSeatID: s.ID,
				Price:          price,
			})
		}

		if err := tx.Create(&items).Error; err != nil {
			return fmt.Errorf("failed to create booking items: %v", err)
		}

		if err := tx.
			Preload("Items.ShowTimeSeat.Seat").
			First(booking, "id = ?", booking.ID).Error; err != nil {
			return err
		}

		return nil
	})

	return booking, err
}

// ---------- ConfirmBooking ----------

func (r *bookingRepository) ConfirmBooking(bookingID uuid.UUID) (*models.Booking, error) {
	var booking models.Booking

	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Items.ShowTimeSeat.Seat").
			First(&booking, "id = ? AND deleted_at IS NULL", bookingID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.NewNotFound("booking not found")
			}
			return err
		}

		if booking.Status != models.BookingStatusHolding {
			return apperror.NewConflict(
				fmt.Sprintf("booking cannot be confirmed, current status: %s", booking.Status),
			)
		}

		now := time.Now()
		if booking.ExpiresAt != nil && booking.ExpiresAt.Before(now) {
			return apperror.NewConflict("booking hold has expired")
		}

		if err := tx.Model(&models.Booking{}).
			Where("id = ?", booking.ID).
			Updates(map[string]interface{}{
				"status":     models.BookingStatusPaid,
				"expires_at": nil,
			}).Error; err != nil {
			return fmt.Errorf("failed to update booking status: %v", err)
		}

		var seatIDs []uuid.UUID
		for _, item := range booking.Items {
			seatIDs = append(seatIDs, item.ShowTimeSeatID)
		}

		if err := tx.Model(&models.ShowTimeSeat{}).
			Where("id IN ?", seatIDs).
			Updates(map[string]interface{}{
				"status":     models.ShowTimeSeatStatusSold,
				"expires_at": nil,
			}).Error; err != nil {
			return fmt.Errorf("failed to update seat status: %v", err)
		}

		booking.Status = models.BookingStatusPaid
		booking.ExpiresAt = nil

		return nil
	})

	if err != nil {
		return nil, err
	}

	return &booking, nil
}

// ---------- CancelBooking ----------

func (r *bookingRepository) CancelBooking(bookingID uuid.UUID) (*models.Booking, error) {
	var booking models.Booking

	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Items.ShowTimeSeat.Seat").
			First(&booking, "id = ? AND deleted_at IS NULL", bookingID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.NewNotFound("booking not found")
			}
			return err
		}

		if booking.Status == models.BookingStatusCanceled {
			return apperror.NewConflict("booking is already canceled")
		}

		if booking.Status == models.BookingStatusExpired {
			return apperror.NewConflict("booking has already expired")
		}

		if booking.Status != models.BookingStatusHolding {
			return apperror.NewConflict(
				fmt.Sprintf("booking cannot be canceled, current status: %s", booking.Status),
			)
		}

		if err := tx.Model(&models.Booking{}).
			Where("id = ?", booking.ID).
			Updates(map[string]interface{}{
				"status":     models.BookingStatusCanceled,
				"expires_at": nil,
			}).Error; err != nil {
			return fmt.Errorf("failed to cancel booking: %v", err)
		}

		var seatIDs []uuid.UUID
		for _, item := range booking.Items {
			seatIDs = append(seatIDs, item.ShowTimeSeatID)
		}

		if err := tx.Model(&models.ShowTimeSeat{}).
			Where("id IN ?", seatIDs).
			Updates(map[string]interface{}{
				"status":     models.ShowTimeSeatStatusAvailable,
				"booking_id": nil,
				"expires_at": nil,
			}).Error; err != nil {
			return fmt.Errorf("failed to release seats: %v", err)
		}

		booking.Status = models.BookingStatusCanceled
		booking.ExpiresAt = nil

		return nil
	})

	if err != nil {
		return nil, err
	}

	return &booking, nil
}

// ---------- GetBookingByID ----------

func (r *bookingRepository) GetBookingByID(bookingID uuid.UUID) (*models.Booking, error) {
	var booking models.Booking

	err := r.db.
		Preload("Items.ShowTimeSeat.Seat").
		First(&booking, "id = ? AND deleted_at IS NULL", bookingID).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperror.NewNotFound("booking not found")
		}
		return nil, apperror.NewInternal("failed to get booking", err)
	}

	return &booking, nil
}

// ---------- GetBookingsByUser ----------

func (r *bookingRepository) GetBookingsByUser(userID uuid.UUID, page, pageSize int) ([]models.Booking, int64, error) {
	var bookings []models.Booking
	var total int64

	query := r.db.Model(&models.Booking{}).Where("user_id = ? AND deleted_at IS NULL", userID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, apperror.NewInternal("failed to count bookings", err)
	}

	offset := (page - 1) * pageSize

	err := r.db.
		Preload("Items.ShowTimeSeat.Seat").
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&bookings).Error

	if err != nil {
		return nil, 0, apperror.NewInternal("failed to get bookings", err)
	}

	return bookings, total, nil
}

// ---------- ReleaseExpiredHolds ----------

func (r *bookingRepository) ReleaseExpiredHolds() (int64, error) {
	now := time.Now()
	var totalReleased int64

	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Find expired bookings
		var expiredBookings []models.Booking
		if err := tx.
			Where("status = ? AND expires_at < ?", models.BookingStatusHolding, now).
			Find(&expiredBookings).Error; err != nil {
			return err
		}

		if len(expiredBookings) == 0 {
			return nil
		}

		var bookingIDs []uuid.UUID
		for _, b := range expiredBookings {
			bookingIDs = append(bookingIDs, b.ID)
		}

		// Update bookings to EXPIRED
		result := tx.Model(&models.Booking{}).
			Where("id IN ?", bookingIDs).
			Update("status", models.BookingStatusExpired)

		if result.Error != nil {
			return result.Error
		}

		totalReleased = result.RowsAffected

		// Release the seats back to AVAILABLE
		if err := tx.Model(&models.ShowTimeSeat{}).
			Where("booking_id IN ? AND status = ?", bookingIDs, models.ShowTimeSeatStatusHolding).
			Updates(map[string]interface{}{
				"status":     models.ShowTimeSeatStatusAvailable,
				"booking_id": nil,
				"expires_at": nil,
			}).Error; err != nil {
			return err
		}

		r.logger.Info("released expired holds",
			zap.Int64("count", totalReleased),
			zap.Time("as_of", now),
		)

		return nil
	})

	return totalReleased, err
}

// ---------- GetDashboardStats ----------

func (r *bookingRepository) GetDashboardStats() (*dto.DashboardStatsResponse, error) {
	stats := &dto.DashboardStatsResponse{}

	// ---- Booking counts by status ----
	type statusCount struct {
		Status string
		Count  int64
	}
	var statusCounts []statusCount
	if err := r.db.Model(&models.Booking{}).
		Where("deleted_at IS NULL").
		Select("status, COUNT(*) as count").
		Group("status").
		Scan(&statusCounts).Error; err != nil {
		return nil, apperror.NewInternal("failed to count bookings by status", err)
	}
	for _, sc := range statusCounts {
		stats.TotalBookings += sc.Count
		switch models.BookingStatus(sc.Status) {
		case models.BookingStatusPaid:
			stats.PaidBookings = sc.Count
		case models.BookingStatusHolding:
			stats.HoldingBookings = sc.Count
		case models.BookingStatusCanceled:
			stats.CanceledBookings = sc.Count
		case models.BookingStatusExpired:
			stats.ExpiredBookings = sc.Count
		}
	}

	// ---- Seat counts by status ----
	type seatStatusCount struct {
		Status string
		Count  int64
	}
	var seatCounts []seatStatusCount
	if err := r.db.Model(&models.ShowTimeSeat{}).
		Select("status, COUNT(*) as count").
		Group("status").
		Scan(&seatCounts).Error; err != nil {
		return nil, apperror.NewInternal("failed to count seats by status", err)
	}
	for _, sc := range seatCounts {
		stats.TotalSeats += sc.Count
		switch models.ShowTimeSeatStatus(sc.Status) {
		case models.ShowTimeSeatStatusAvailable:
			stats.AvailableSeats = sc.Count
		case models.ShowTimeSeatStatusHolding:
			stats.HoldingSeats = sc.Count
		case models.ShowTimeSeatStatusSold:
			stats.SoldSeats = sc.Count
			stats.TicketsSold = sc.Count
		}
	}

	// ---- Total revenue from PAID bookings ----
	type revenueResult struct {
		Total float64
	}
	var revResult revenueResult
	if err := r.db.Model(&models.BookingItem{}).
		Joins("JOIN bookings ON bookings.id = booking_items.booking_id").
		Where("bookings.status = ? AND bookings.deleted_at IS NULL", models.BookingStatusPaid).
		Select("COALESCE(SUM(booking_items.price), 0) as total").
		Scan(&revResult).Error; err != nil {
		return nil, apperror.NewInternal("failed to sum revenue", err)
	}
	stats.TotalRevenue = revResult.Total

	// ---- Daily revenue series for last 7 days ----
	type dailyRevenue struct {
		Day     string
		Revenue float64
	}
	var dailySeries []dailyRevenue
	sevenDaysAgo := time.Now().UTC().AddDate(0, 0, -6).Truncate(24 * time.Hour)
	if err := r.db.Model(&models.BookingItem{}).
		Joins("JOIN bookings ON bookings.id = booking_items.booking_id").
		Where("bookings.status = ? AND bookings.deleted_at IS NULL AND bookings.created_at >= ?",
			models.BookingStatusPaid, sevenDaysAgo).
		Select("TO_CHAR(DATE(bookings.created_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD') as day, COALESCE(SUM(booking_items.price), 0) as revenue").
		Group("day").
		Order("day ASC").
		Scan(&dailySeries).Error; err != nil {
		return nil, apperror.NewInternal("failed to query daily revenue", err)
	}

	// Build a complete 7-day series with zero-fills for missing days
	revenueByDay := make(map[string]float64, len(dailySeries))
	for _, d := range dailySeries {
		revenueByDay[d.Day] = d.Revenue
	}
	series := make([]dto.DashboardRevenuePoint, 0, 7)
	for i := 6; i >= 0; i-- {
		day := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		series = append(series, dto.DashboardRevenuePoint{
			Date:    day,
			Revenue: revenueByDay[day],
		})
	}
	stats.RevenueSeries = series

	return stats, nil
}
