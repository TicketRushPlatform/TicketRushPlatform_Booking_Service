package dto

import (
	"time"
)

type BookingResponse struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Status     string     `json:"status"`
	ShowTimeID string     `json:"showtime_id"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`

	Items       []BookingItemDTO `json:"items"`
	TotalAmount string           `json:"total_amount"`
	CreatedAt   time.Time        `json:"created_at"`
}

type BookingItemDTO struct {
	SeatID string `json:"seat_id"`
	Row    string `json:"row"`
	Number int    `json:"number"`
	Price  string `json:"price"`
}

type SeatStatusDTO struct {
	SeatID    string     `json:"seat_id"`
	Row       string     `json:"row"`
	Number    int        `json:"number"`
	SeatClass string     `json:"seat_class"`
	Status    string     `json:"status"`
	Price     string     `json:"price,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type SeatsStatusResponse struct {
	ShowtimeID string          `json:"showtime_id"`
	Seats      []SeatStatusDTO `json:"seats"`
	Total      int             `json:"total"`
	Available  int             `json:"available"`
	Holding    int             `json:"holding"`
	Sold       int             `json:"sold"`
}

type PaginatedResponse struct {
	Data       interface{} `json:"data"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalItems int64       `json:"total_items"`
	TotalPages int         `json:"total_pages"`
}

type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type SuccessResponse struct {
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}
