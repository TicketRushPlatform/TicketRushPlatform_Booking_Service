package models

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestBaseBeforeCreate(t *testing.T) {
	b := Base{}
	if err := b.BeforeCreate(&gorm.DB{}); err != nil {
		t.Fatalf("BeforeCreate err=%v", err)
	}
	if b.ID == uuid.Nil {
		t.Fatalf("expected generated ID")
	}
}

func TestBookingToDTO(t *testing.T) {
	now := time.Now().UTC()
	booking := Booking{
		Base:       Base{ID: uuid.New(), CreatedAt: now, UpdatedAt: now},
		ShowTimeID: uuid.New(),
		Status:     BookingStatusHolding,
		Items: []BookingItem{
			{
				Price: decimal.NewFromInt(100),
				ShowTimeSeat: ShowTimeSeat{
					SeatID: uuid.New(),
					Seat: Seat{
						Row:    "A",
						Number: 1,
					},
				},
			},
		},
	}

	dto := booking.ToDTO()
	if dto.ID == "" || dto.TotalAmount != "100" || len(dto.Items) != 1 {
		t.Fatalf("unexpected dto: %+v", dto)
	}
}

func TestSeatPricingTableNameMatchesMigration(t *testing.T) {
	if got := (SeatPricing{}).TableName(); got != "seat_pricing" {
		t.Fatalf("SeatPricing table name = %q, want %q", got, "seat_pricing")
	}
}
