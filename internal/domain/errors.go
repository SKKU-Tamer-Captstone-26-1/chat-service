package domain

import "errors"

var (
	ErrNotFound            = errors.New("not found")
	ErrRoomInactive        = errors.New("room is inactive")
	ErrAlreadyExists       = errors.New("already exists")
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrNotConfigured       = errors.New("not configured")
	ErrPermissionDenied    = errors.New("permission denied")
	ErrInvalidState        = errors.New("invalid state")
	ErrRemovedCannotRejoin = errors.New("removed user cannot rejoin")
)
