package handler

import (
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
)

// validate is a package-level instance. validator caches struct metadata, so
// reusing one is materially faster than constructing per-call.
var validate = validator.New(validator.WithRequiredStructEnabled())

// validateStruct runs struct tag validation and returns a human-friendly
// message keyed to the first failing field. Caller pairs the returned msg
// with WriteError(400, msg).
func validateStruct(v any) (string, bool) {
	err := validate.Struct(v)
	if err == nil {
		return "", true
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) || len(verrs) == 0 {
		return "invalid request", false
	}
	first := verrs[0]
	switch first.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", first.Field()), false
	case "min":
		return fmt.Sprintf("%s must be at least %s characters", first.Field(), first.Param()), false
	case "max":
		return fmt.Sprintf("%s must be at most %s characters", first.Field(), first.Param()), false
	default:
		return fmt.Sprintf("%s is invalid", first.Field()), false
	}
}
