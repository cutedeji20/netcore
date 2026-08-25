package customers

import (
	"errors"
	"net/mail"
	"strings"
)

var (
	ErrInvalidInput   = errors.New("customers: invalid customer profile")
	ErrNotFound       = errors.New("customers: customer not found")
	ErrDuplicateEmail = errors.New("customers: customer e-mail already exists")
)

// WriteInput is deliberately limited to customer profile data. Creating or
// editing a profile must never create identity credentials or staff access.
type WriteInput struct {
	FirstName string
	LastName  string
	Email     string
	Phone     string
}

// MutationActor carries trusted request metadata for an audit event.
type MutationActor struct {
	UserID    string
	IP        string
	UserAgent string
}

func (in *WriteInput) NormalizeAndValidate() error {
	if in == nil {
		return ErrInvalidInput
	}
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = strings.TrimSpace(in.LastName)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Phone = strings.TrimSpace(in.Phone)
	if !validName(in.FirstName) || !validName(in.LastName) || !validEmail(in.Email) || !validPhone(in.Phone) {
		return ErrInvalidInput
	}
	return nil
}

func validName(value string) bool {
	return value != "" && len(value) <= 120
}

func validEmail(value string) bool {
	if value == "" || len(value) > 254 {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Count(value, "@") == 1
}

// Phone remains optional, but when captured it is a bounded E.164 number so
// staff cannot persist unbounded or ambiguous identity data.
func validPhone(value string) bool {
	if value == "" {
		return true
	}
	if len(value) < 8 || len(value) > 16 || value[0] != '+' {
		return false
	}
	for _, character := range value[1:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
