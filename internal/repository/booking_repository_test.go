package repository

import (
	"booking_api/internal/dto"
	"booking_api/internal/models"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRepo(t *testing.T) BookingRepository {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	if err := db.AutoMigrate(
		&models.Venue{},
		&models.SeatMap{},
		&models.Seat{},
		&models.ShowTime{},
		&models.Booking{},
		&models.ShowTimeSeat{},
		&models.BookingItem{},
		&models.SeatPricing{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return NewBookingRepository(db, zap.NewNop())
}

func setupMockRepo(t *testing.T) (BookingRepository, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}
	return NewBookingRepository(gdb, zap.NewNop()), mock
}

func seedShowtimeWithSeats(t *testing.T, repo BookingRepository, seatCount int) (uuid.UUID, []models.ShowTimeSeat, []uuid.UUID) {
	t.Helper()
	r := repo.(*bookingRepository)

	venue := models.Venue{Name: "v1", Address: "a1"}
	if err := r.db.Create(&venue).Error; err != nil {
		t.Fatalf("create venue: %v", err)
	}

	seatMap := models.SeatMap{Name: "main", VenueID: venue.ID}
	if err := r.db.Create(&seatMap).Error; err != nil {
		t.Fatalf("create seat map: %v", err)
	}

	showTime := models.ShowTime{
		EventID:   uuid.New(),
		SeatMapID: seatMap.ID,
		VenueID:   venue.ID,
		StartTime: time.Now().Add(time.Hour),
		EndTime:   time.Now().Add(2 * time.Hour),
		Base:      models.Base{},
	}
	if err := r.db.Create(&showTime).Error; err != nil {
		t.Fatalf("create showtime: %v", err)
	}

	showSeats := make([]models.ShowTimeSeat, 0, seatCount)
	seatIDs := make([]uuid.UUID, 0, seatCount)
	for i := 0; i < seatCount; i++ {
		seat := models.Seat{
			SeatMapID: seatMap.ID,
			Row:       "A",
			Number:    i + 1,
			SeatClass: models.SeatClassStandard,
			Price:     decimal.NewFromInt(100),
		}
		if err := r.db.Create(&seat).Error; err != nil {
			t.Fatalf("create seat: %v", err)
		}

		sts := models.ShowTimeSeat{
			ShowTimeID: showTime.ID,
			SeatID:     seat.ID,
			Status:     models.ShowTimeSeatStatusAvailable,
		}
		if err := r.db.Create(&sts).Error; err != nil {
			t.Fatalf("create showtime seat: %v", err)
		}

		pr := models.SeatPricing{
			ShowTimeID: showTime.ID,
			SeatID:     seat.ID,
			Price:      decimal.NewFromInt(100),
		}
		if err := r.db.Create(&pr).Error; err != nil {
			t.Fatalf("create seat pricing: %v", err)
		}

		showSeats = append(showSeats, sts)
		seatIDs = append(seatIDs, seat.ID)
	}

	return showTime.ID, showSeats, seatIDs
}

func TestNewBookingRepository(t *testing.T) {
	repo := setupRepo(t)
	if repo == nil {
		t.Fatalf("expected non-nil repo")
	}
}

func TestBookingRepository_HoldAndGetBooking(t *testing.T) {
	repo := setupRepo(t)
	showtimeID, _, seatIDs := seedShowtimeWithSeats(t, repo, 2)
	userID := uuid.New()

	booking, err := repo.HoldSeats(dto.HoldSeatsRequest{
		UserID:     userID,
		ShowtimeID: showtimeID,
		SeatIDs:    seatIDs,
	})
	if err != nil {
		t.Fatalf("HoldSeats() err=%v", err)
	}
	if booking.Status != models.BookingStatusHolding || len(booking.Items) != 2 {
		t.Fatalf("unexpected booking after hold: %+v", booking)
	}

	got, err := repo.GetBookingByID(booking.ID)
	if err != nil {
		t.Fatalf("GetBookingByID() err=%v", err)
	}
	if got.ID != booking.ID {
		t.Fatalf("booking id mismatch")
	}

	_, err = repo.GetBookingByID(uuid.New())
	if err == nil {
		t.Fatalf("expected not found error")
	}
}

func TestBookingRepository_HoldSeatsConflict(t *testing.T) {
	repo := setupRepo(t)
	showtimeID, showSeats, seatIDs := seedShowtimeWithSeats(t, repo, 2)
	r := repo.(*bookingRepository)

	if err := r.db.Model(&models.ShowTimeSeat{}).
		Where("id = ?", showSeats[0].ID).
		Update("status", models.ShowTimeSeatStatusSold).Error; err != nil {
		t.Fatalf("update seat status: %v", err)
	}

	_, err := repo.HoldSeats(dto.HoldSeatsRequest{
		UserID:     uuid.New(),
		ShowtimeID: showtimeID,
		SeatIDs:    seatIDs,
	})
	if err == nil {
		t.Fatalf("expected conflict error")
	}
}

func TestBookingRepository_HoldSeatsPriceMissing(t *testing.T) {
	repo := setupRepo(t)
	showtimeID, _, seatIDs := seedShowtimeWithSeats(t, repo, 1)
	r := repo.(*bookingRepository)

	if err := r.db.Where("show_time_id = ?", showtimeID).Delete(&models.SeatPricing{}).Error; err != nil {
		t.Fatalf("delete seat pricing: %v", err)
	}

	_, err := repo.HoldSeats(dto.HoldSeatsRequest{
		UserID:     uuid.New(),
		ShowtimeID: showtimeID,
		SeatIDs:    seatIDs,
	})
	if err == nil {
		t.Fatalf("expected missing price error")
	}
}

func TestBookingRepository_ConfirmBooking(t *testing.T) {
	repo := setupRepo(t)
	showtimeID, showSeats, seatIDs := seedShowtimeWithSeats(t, repo, 2)
	userID := uuid.New()

	booking, err := repo.HoldSeats(dto.HoldSeatsRequest{
		UserID:     userID,
		ShowtimeID: showtimeID,
		SeatIDs:    seatIDs,
	})
	if err != nil {
		t.Fatalf("HoldSeats() err=%v", err)
	}

	confirmed, err := repo.ConfirmBooking(booking.ID)
	if err != nil {
		t.Fatalf("ConfirmBooking() err=%v", err)
	}
	if confirmed.Status != models.BookingStatusPaid {
		t.Fatalf("expected PAID, got %s", confirmed.Status)
	}

	seats, err := repo.GetSeatsStatus(showtimeID)
	if err != nil {
		t.Fatalf("GetSeatsStatus() err=%v", err)
	}
	for _, s := range seats {
		if s.Status != models.ShowTimeSeatStatusSold {
			t.Fatalf("expected SOLD seat status")
		}
	}

	_, err = repo.ConfirmBooking(uuid.New())
	if err == nil {
		t.Fatalf("expected not found")
	}

	_, err = repo.ConfirmBooking(booking.ID)
	if err == nil {
		t.Fatalf("expected conflict when confirming paid booking")
	}

	r := repo.(*bookingRepository)
	expired := models.Booking{
		UserID:     uuid.New(),
		ShowTimeID: showtimeID,
		Status:     models.BookingStatusHolding,
	}
	past := time.Now().Add(-time.Hour)
	expired.ExpiresAt = &past
	if err := r.db.Create(&expired).Error; err != nil {
		t.Fatalf("create expired booking: %v", err)
	}
	item := models.BookingItem{
		BookingID:      expired.ID,
		ShowTimeSeatID: showSeats[0].ID,
		Price:          decimal.NewFromInt(100),
	}
	if err := r.db.Create(&item).Error; err != nil {
		t.Fatalf("create booking item: %v", err)
	}

	_, err = repo.ConfirmBooking(expired.ID)
	if err == nil {
		t.Fatalf("expected expired conflict")
	}
}

func TestBookingRepository_CancelBooking(t *testing.T) {
	repo := setupRepo(t)
	showtimeID, _, seatIDs := seedShowtimeWithSeats(t, repo, 1)
	booking, err := repo.HoldSeats(dto.HoldSeatsRequest{
		UserID:     uuid.New(),
		ShowtimeID: showtimeID,
		SeatIDs:    seatIDs,
	})
	if err != nil {
		t.Fatalf("HoldSeats err=%v", err)
	}

	canceled, err := repo.CancelBooking(booking.ID)
	if err != nil {
		t.Fatalf("CancelBooking err=%v", err)
	}
	if canceled.Status != models.BookingStatusCanceled {
		t.Fatalf("expected CANCELED")
	}

	_, err = repo.CancelBooking(booking.ID)
	if err == nil {
		t.Fatalf("expected already canceled conflict")
	}

	_, err = repo.CancelBooking(uuid.New())
	if err == nil {
		t.Fatalf("expected not found")
	}

	r := repo.(*bookingRepository)
	expired := models.Booking{
		UserID:     uuid.New(),
		ShowTimeID: showtimeID,
		Status:     models.BookingStatusExpired,
	}
	if err := r.db.Create(&expired).Error; err != nil {
		t.Fatalf("create expired booking: %v", err)
	}
	_, err = repo.CancelBooking(expired.ID)
	if err == nil {
		t.Fatalf("expected expired conflict")
	}
}

func TestBookingRepository_GetBookingsByUserAndReleaseExpired(t *testing.T) {
	repo := setupRepo(t)
	showtimeID, _, seatIDs := seedShowtimeWithSeats(t, repo, 2)
	userID := uuid.New()

	_, err := repo.HoldSeats(dto.HoldSeatsRequest{
		UserID:     userID,
		ShowtimeID: showtimeID,
		SeatIDs:    []uuid.UUID{seatIDs[0]},
	})
	if err != nil {
		t.Fatalf("HoldSeats err=%v", err)
	}

	bookings, total, err := repo.GetBookingsByUser(userID, 1, 10)
	if err != nil {
		t.Fatalf("GetBookingsByUser err=%v", err)
	}
	if total < 1 || len(bookings) < 1 {
		t.Fatalf("expected bookings for user")
	}

	r := repo.(*bookingRepository)
	exp := models.Booking{
		UserID:     userID,
		ShowTimeID: showtimeID,
		Status:     models.BookingStatusHolding,
	}
	past := time.Now().Add(-2 * time.Hour)
	exp.ExpiresAt = &past
	if err := r.db.Create(&exp).Error; err != nil {
		t.Fatalf("create expired booking: %v", err)
	}
	if err := r.db.Model(&models.ShowTimeSeat{}).
		Where("show_time_id = ? AND seat_id = ?", showtimeID, seatIDs[1]).
		Updates(map[string]interface{}{
			"status":     models.ShowTimeSeatStatusHolding,
			"booking_id": exp.ID,
			"expires_at": past,
		}).Error; err != nil {
		t.Fatalf("hold seat for expired booking: %v", err)
	}

	released, err := repo.ReleaseExpiredHolds()
	if err != nil {
		t.Fatalf("ReleaseExpiredHolds err=%v", err)
	}
	if released < 1 {
		t.Fatalf("expected released >= 1, got %d", released)
	}

	seats, err := repo.GetSeatsStatus(showtimeID)
	if err != nil {
		t.Fatalf("GetSeatsStatus err=%v", err)
	}
	if len(seats) == 0 {
		t.Fatalf("expected seats")
	}
}

func TestBookingRepository_GetSeatsStatusExpiredHolding(t *testing.T) {
	repo := setupRepo(t)
	showtimeID, showSeats, _ := seedShowtimeWithSeats(t, repo, 1)
	r := repo.(*bookingRepository)

	past := time.Now().Add(-time.Hour)
	if err := r.db.Model(&models.ShowTimeSeat{}).Where("id = ?", showSeats[0].ID).Updates(map[string]interface{}{
		"status":     models.ShowTimeSeatStatusHolding,
		"booking_id": uuid.New(),
		"expires_at": past,
	}).Error; err != nil {
		t.Fatalf("set holding expired: %v", err)
	}

	seats, err := repo.GetSeatsStatus(showtimeID)
	if err != nil {
		t.Fatalf("GetSeatsStatus err=%v", err)
	}
	if len(seats) != 1 || seats[0].Status != models.ShowTimeSeatStatusAvailable {
		t.Fatalf("expected expired holding to be returned as available")
	}
}

func TestBookingRepository_ErrorPathsWithMock(t *testing.T) {
	repo, mock := setupMockRepo(t)
	id := uuid.New()

	t.Run("hold seats select error", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT .* FROM "show_time_seats"`).WillReturnError(errors.New("select error"))
		mock.ExpectRollback()
		_, err := repo.HoldSeats(dto.HoldSeatsRequest{
			UserID:     uuid.New(),
			ShowtimeID: uuid.New(),
			SeatIDs:    []uuid.UUID{uuid.New()},
		})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("get seats status query error", func(t *testing.T) {
		mock.ExpectQuery(`SELECT .* FROM "show_time_seats"`).WillReturnError(errors.New("db error"))
		_, err := repo.GetSeatsStatus(id)
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("get booking by id query error", func(t *testing.T) {
		mock.ExpectQuery(`SELECT .* FROM "bookings"`).WillReturnError(errors.New("db error"))
		_, err := repo.GetBookingByID(id)
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("get bookings count error", func(t *testing.T) {
		mock.ExpectQuery(`SELECT count\(\*\) FROM "bookings"`).WillReturnError(errors.New("count error"))
		_, _, err := repo.GetBookingsByUser(id, 1, 10)
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("release expired holds find error", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT .* FROM "bookings"`).WillReturnError(errors.New("find error"))
		mock.ExpectRollback()
		_, err := repo.ReleaseExpiredHolds()
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("release expired holds none", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT .* FROM "bookings"`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectCommit()
		released, err := repo.ReleaseExpiredHolds()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if released != 0 {
			t.Fatalf("expected 0 released")
		}
	})

	t.Run("release expired holds update error", func(t *testing.T) {
		expiredID := uuid.New()
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT .* FROM "bookings"`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(expiredID))
		mock.ExpectExec(`UPDATE "bookings" SET "status"`).
			WillReturnError(errors.New("update error"))
		mock.ExpectRollback()
		_, err := repo.ReleaseExpiredHolds()
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}
