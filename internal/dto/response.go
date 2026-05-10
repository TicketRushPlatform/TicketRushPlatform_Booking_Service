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

type QueueStatusResponse struct {
	ShowtimeID   string `json:"showtime_id"`
	UserID       string `json:"user_id"`
	Position     int64  `json:"position"`
	TotalWaiting int64  `json:"total_waiting"`
	InQueue      bool   `json:"in_queue"`
	CanEnter     bool   `json:"can_enter"`
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

// DashboardRevenuePoint represents revenue aggregated for a single day.
type DashboardRevenuePoint struct {
	Date    string  `json:"date"`
	Revenue float64 `json:"revenue"`
}

// DashboardStatsResponse is the response payload for the admin dashboard endpoint.
type DashboardStatsResponse struct {
	// Booking summary
	TotalBookings    int64 `json:"total_bookings"`
	PaidBookings     int64 `json:"paid_bookings"`
	HoldingBookings  int64 `json:"holding_bookings"`
	CanceledBookings int64 `json:"canceled_bookings"`
	ExpiredBookings  int64 `json:"expired_bookings"`

	// Ticket / seat summary
	TicketsSold    int64 `json:"tickets_sold"`
	TotalSeats     int64 `json:"total_seats"`
	AvailableSeats int64 `json:"available_seats"`
	HoldingSeats   int64 `json:"holding_seats"`
	SoldSeats      int64 `json:"sold_seats"`

	// Revenue (total from confirmed bookings)
	TotalRevenue float64 `json:"total_revenue"`

	// Last-7-days daily revenue series (oldest → newest)
	RevenueSeries []DashboardRevenuePoint `json:"revenue_series"`
}
