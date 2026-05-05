package dto

import "github.com/google/uuid"

type HoldSeatsRequest struct {
	UserID     uuid.UUID   `json:"user_id" binding:"required"`
	ShowtimeID uuid.UUID   `json:"showtime_id" binding:"required"`
	SeatIDs    []uuid.UUID `json:"seat_ids" binding:"required,min=1"`
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
