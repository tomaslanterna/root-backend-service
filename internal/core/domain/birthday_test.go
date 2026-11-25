package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestBirthdayMonthDays(t *testing.T) {
	tests := []struct {
		name string
		date time.Time
		want []string
	}{
		{"regular day", time.Date(2026, time.November, 25, 0, 0, 0, 0, time.UTC), []string{"11-25"}},
		{"leap birthday greeted on 28/02 of a common year", time.Date(2026, time.February, 28, 0, 0, 0, 0, time.UTC), []string{"02-28", "02-29"}},
		{"century common year", time.Date(2100, time.February, 28, 0, 0, 0, 0, time.UTC), []string{"02-28", "02-29"}},
		{"28/02 of a leap year waits for the 29th", time.Date(2028, time.February, 28, 0, 0, 0, 0, time.UTC), []string{"02-28"}},
		{"29/02 of a leap year", time.Date(2028, time.February, 29, 0, 0, 0, 0, time.UTC), []string{"02-29"}},
		{"400-year leap year", time.Date(2000, time.February, 28, 0, 0, 0, 0, time.UTC), []string{"02-28"}},
		{"01/03 never includes 29/02", time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), []string{"03-01"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BirthdayMonthDays(tt.date); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("BirthdayMonthDays(%s) = %v, want %v", tt.date.Format("2006-01-02"), got, tt.want)
			}
		})
	}
}

func TestBirthdayContract(t *testing.T) {
	if got := BirthdayDedupeKey(2026); got != "birthday:2026" {
		t.Fatalf("dedupe key = %q", got)
	}
	if BirthdayNotificationType != "user.birthday" || BirthdayNotificationCategory != NotificationAccount {
		t.Fatalf("unexpected type/category: %s/%s", BirthdayNotificationType, BirthdayNotificationCategory)
	}
}
