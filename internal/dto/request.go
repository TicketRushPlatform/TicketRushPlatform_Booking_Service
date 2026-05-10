package dto

import (
	"time"

	"github.com/google/uuid"
)

type HoldSeatsRequest struct {
	UserID     uuid.UUID   `json:"user_id" binding:"required"`
	ShowtimeID uuid.UUID   `json:"showtime_id" binding:"required"`
	SeatIDs    []uuid.UUID `json:"seat_ids" binding:"required,min=1"`
	// HoldExpiresAt is set by the HTTP handler from the booking-room session deadline (not from JSON).
	HoldExpiresAt time.Time `json:"-"`
}

type ConfirmBookingRequest struct {
	PaymentMethod string `json:"payment_method" binding:"required"`
}

type CancelBookingRequest struct {
	Reason string `json:"reason"`
}

type PaginationQuery struct {
	Page     int `form:"page,default=1" binding:"min=1"`
	PageSize int `form:"page_size,default=20" binding:"min=1,max=100"`
}
