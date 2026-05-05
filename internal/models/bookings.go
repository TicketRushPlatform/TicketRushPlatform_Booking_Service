package models

import (
	"booking_api/internal/dto"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type ShowTimeSeatStatus string

const (
	ShowTimeSeatStatusHolding   ShowTimeSeatStatus = "HOLDING"
	ShowTimeSeatStatusAvailable ShowTimeSeatStatus = "AVAILABLE"
	ShowTimeSeatStatusSold      ShowTimeSeatStatus = "SOLD"
)

type BookingStatus string

const (
	BookingStatusHolding  BookingStatus = "HOLDING"
	BookingStatusPaid     BookingStatus = "PAID"
	BookingStatusCanceled BookingStatus = "CANCELED"
	BookingStatusExpired  BookingStatus = "EXPIRED"
)

type ShowTime struct {
	Base

	EventID   uuid.UUID
	VenueID   uuid.UUID
	SeatMapID uuid.UUID

	StartTime time.Time
	EndTime   time.Time

	ShowTimeSeats []ShowTimeSeat `gorm:"foreignKey:ShowTimeID"`
}

type ShowTimeSeat struct {
	Base

	ShowTimeID uuid.UUID `gorm:"type:uuid;uniqueIndex:idx_showtime_seat;index:idx_showtime_status"`
	SeatID     uuid.UUID `gorm:"type:uuid;uniqueIndex:idx_showtime_seat"`

	Status ShowTimeSeatStatus `gorm:"type:varchar(20);index:idx_showtime_status"`

	BookingID *uuid.UUID `gorm:"type:uuid;index:idx_booking_id"`
	ExpiresAt *time.Time `gorm:"index"`

	ShowTime ShowTime `gorm:"foreignKey:ShowTimeID;references:ID"`
	Booking  *Booking `gorm:"foreignKey:BookingID;references:ID"`
	Seat     Seat     `gorm:"foreignKey:SeatID;references:ID;constraint:OnDelete:CASCADE"`
}

type Booking struct {
	Base

	UserID     uuid.UUID `gorm:"type:uuid;index" json:"user_id"`
	ShowTimeID uuid.UUID `gorm:"type:uuid;index" json:"showtime_id"`

	Status    BookingStatus `gorm:"type:varchar(20);index" json:"status"`
	ExpiresAt *time.Time    `gorm:"index"`

	Items    []BookingItem `gorm:"foreignKey:BookingID"`
	ShowTime ShowTime      `gorm:"foreignKey:ShowTimeID"`
}

type SeatPricing struct {
	Base

	ShowTimeID uuid.UUID `gorm:"type:uuid;index:idx_showtime_seat_price,unique"`
	SeatID     uuid.UUID `gorm:"type:uuid;index:idx_showtime_seat_price,unique"`

	Price decimal.Decimal `gorm:"type:numeric(10,2);not null"`

	// relations
	ShowTime ShowTime `gorm:"foreignKey:ShowTimeID;references:ID"`
	Seat     Seat     `gorm:"foreignKey:SeatID;references:ID"`
}

func (b *Booking) ToDTO() *dto.BookingResponse {
	totalAmount := decimal.NewFromInt(0)

	items := make([]dto.BookingItemDTO, 0, len(b.Items))

	for _, x := range b.Items {
		totalAmount = totalAmount.Add(x.Price)

		items = append(items, dto.BookingItemDTO{
			SeatID: x.ShowTimeSeat.SeatID.String(),
			Row:    x.ShowTimeSeat.Seat.Row,
			Number: x.ShowTimeSeat.Seat.Number,
			Price:  x.Price.String(),
		})
	}

	return &dto.BookingResponse{
		ID:          b.ID.String(),
		ShowTimeID:  b.ShowTimeID.String(),
		Status:      string(b.Status),
		ExpiresAt:   b.ExpiresAt,
		Items:       items,
		TotalAmount: totalAmount.String(),
		CreatedAt:   b.CreatedAt,
	}
}

type BookingItem struct {
	Base

	BookingID      uuid.UUID `gorm:"type:uuid;index;uniqueIndex:idx_booking_seat" json:"booking_id"`
	ShowTimeSeatID uuid.UUID `gorm:"type:uuid;uniqueIndex:idx_booking_seat" json:"showtime_seat_id"`

	Price decimal.Decimal `gorm:"type:numeric(10,2);not null"`

	Booking      Booking      `gorm:"foreignKey:BookingID"`
	ShowTimeSeat ShowTimeSeat `gorm:"foreignKey:ShowTimeSeatID"`
}
