package handlers

import (
	"errors"
	"io"
	"net/http"
	"strings"
)

var (
	errImageEmpty       = errors.New("imagen vacía")
	errImageTooLarge    = errors.New("imagen demasiado grande")
	errImageUnsupported = errors.New("formato de imagen no soportado")
)

// imageExtensions son los únicos tipos que se guardan en el bucket público. El tipo se decide
// por el contenido (http.DetectContentType), nunca por el Content-Type ni el nombre que manda el cliente.
var imageExtensions = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
}

// readImageUpload lee como máximo maxBytes y devuelve los bytes, el tipo real y la extensión
// que hay que usar en la key. allowGIF habilita GIF (los posts los aceptan sin recomprimir).
func readImageUpload(file io.Reader, maxBytes int64, allowGIF bool) ([]byte, string, string, error) {
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(data) == 0 {
		return nil, "", "", errImageEmpty
	}
	if int64(len(data)) > maxBytes {
		return nil, "", "", errImageTooLarge
	}
	contentType := http.DetectContentType(data)
	ext, ok := imageExtensions[contentType]
	if !ok && allowGIF && contentType == "image/gif" {
		ext, ok = "gif", true
	}
	if !ok {
		return nil, "", "", errImageUnsupported
	}
	return data, contentType, ext, nil
}

// imageUploadErrorStatus traduce los errores de readImageUpload a un código HTTP y un mensaje.
func imageUploadErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, errImageTooLarge):
		return http.StatusRequestEntityTooLarge, "La imagen es demasiado grande"
	case errors.Is(err, errImageUnsupported):
		return http.StatusUnsupportedMediaType, "Formato no soportado: usá JPG, PNG o WebP"
	default:
		return http.StatusBadRequest, "No se pudo leer la imagen"
	}
}

// hasRole compara el rol del usuario sin distinguir mayúsculas ni espacios.
func hasRole(role string, allowed ...string) bool {
	role = strings.TrimSpace(role)
	for _, candidate := range allowed {
		if strings.EqualFold(role, candidate) {
			return true
		}
	}
	return false
}
