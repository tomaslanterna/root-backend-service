package domain

import (
	"errors"
	"strings"
	"unicode"
)

// ErrInvalidFullName: el nombre tiene que tener nombre y apellido.
var ErrInvalidFullName = errors.New("ingresá tu nombre y apellido")

// NormalizeFullName saca espacios de más: "  Juan   Pérez " -> "Juan Pérez".
func NormalizeFullName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// IsFullName indica si el nombre tiene nombre y apellido: al menos dos palabras, cada una con dos letras o
// más, y solo letras, apóstrofos, guiones o puntos ("María José O'Neil", "Ana Pérez-Gómez").
func IsFullName(name string) bool {
	words := strings.Fields(name)
	if len(words) < 2 {
		return false
	}
	for _, word := range words {
		letters := 0
		for _, r := range word {
			switch {
			case unicode.IsLetter(r):
				letters++
			case r == '\'' || r == '’' || r == '-' || r == '.':
			default:
				return false
			}
		}
		if letters < 2 {
			return false
		}
	}
	return true
}
