package domain

import (
	"strconv"
	"time"
)

// Contrato de la notificación de cumpleaños (lo consume el frontend tal cual).
// El título es "¡Feliz cumpleaños, @{username}!" y data es {"username": ...}.
// Va en "account" para que el usuario pueda silenciarla desde sus preferencias.
const (
	BirthdayNotificationType     = "user.birthday"
	BirthdayNotificationCategory = NotificationAccount
	BirthdayNotificationBody     = "Desde root te deseamos lo mejor 🎉"
)

// BirthdayDedupeKey limita el saludo a uno por usuario y año calendario.
func BirthdayDedupeKey(year int) string {
	return "birthday:" + strconv.Itoa(year)
}

// BirthdayMonthDays devuelve los sufijos "MM-DD" de users.dob que cumplen en la
// fecha de calendario dada. Quien nació un 29/02 se saluda el 28/02 de los años
// no bisiestos.
func BirthdayMonthDays(date time.Time) []string {
	year, month, day := date.Date()
	days := []string{date.Format("01-02")}
	if month == time.February && day == 28 && !isLeapYear(year) {
		days = append(days, "02-29")
	}
	return days
}

func isLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}
