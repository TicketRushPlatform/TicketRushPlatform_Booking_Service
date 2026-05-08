package models

import (
	"github.com/google/uuid"
)

type SeatClass string

const (
	SeatClassStandard SeatClass = "STANDARD"
	SeatClassVIP      SeatClass = "VIP"
	SeatClassPremium  SeatClass = "PREMIUM"
	SeatClassDeluxe   SeatClass = "DELUXE"
)

type Seat struct {
	Base

	SeatMapID uuid.UUID `gorm:"type:uuid;uniqueIndex:idx_seat_unique"`

	Row    string `gorm:"size:10;not null;uniqueIndex:idx_seat_unique" json:"row"`
	Number int    `gorm:"not null;uniqueIndex:idx_seat_unique" json:"number"`

	SeatClass SeatClass `gorm:"type:varchar(20);not null;index" json:"seat_class"`

	SeatMap SeatMap `gorm:"foreignKey:SeatMapID"`
}

type SeatMap struct {
	Base

	Name    string    `gorm:"not null" json:"name"`
	VenueID uuid.UUID `gorm:"type:uuid;index"`

	Venue Venue `gorm:"foreignKey:VenueID"`

	Seats []Seat `gorm:"foreignKey:SeatMapID"`
}

type Venue struct {
	Base

	Name    string `gorm:"not null" json:"name"`
	Address string `gorm:"not null" json:"address"`

	SeatMaps []SeatMap `gorm:"foreignKey:VenueID"`
}
