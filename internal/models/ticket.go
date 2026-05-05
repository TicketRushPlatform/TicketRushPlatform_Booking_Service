package models

import (
	"time"

	"github.com/google/uuid"
)

type Ticket struct {
	Base

	BookingID      uuid.UUID `gorm:"type:uuid;index;not null" json:"booking_id"`
	ShowTimeSeatID uuid.UUID `gorm:"type:uuid;uniqueIndex;not null" json:"showtime_seat_id"`

	TicketCode string     `gorm:"type:varchar(50);uniqueIndex;not null" json:"ticket_code"`
	IssuedAt   time.Time  `gorm:"not null" json:"issued_at"`
	QRCode     string     `gorm:"type:text" json:"qr_code,omitempty"`
	UsedAt     *time.Time `json:"used_at,omitempty"`

	// Relations
	Booking      Booking      `gorm:"foreignKey:BookingID;references:ID"`
	ShowTimeSeat ShowTimeSeat `gorm:"foreignKey:ShowTimeSeatID;references:ID"`
}
