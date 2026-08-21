package domain

import "errors"

var (
	// ErrPiscineNotFound is returned when an unknown PiscineType is requested.
	ErrPiscineNotFound = errors.New("piscine type not found")

	// ErrTemplateNotFound is returned when a template key has no matching file.
	ErrTemplateNotFound = errors.New("template not found")

	// ErrTokenRefresh is returned when the 01-edu token refresh fails.
	ErrTokenRefresh = errors.New("failed to refresh 01-edu token")

	// ErrGraphQL is returned when a GraphQL query fails.
	ErrGraphQL = errors.New("graphql query error")

	// ErrNoCampuses is returned when the platform has no campus objects to process.
	ErrNoCampuses = errors.New("no campuses found")

	// ErrNoActivePiscine is returned when a piscine has no currently running
	// event on the platform (it has finished, or has not started yet). It is a
	// normal state, not a failure, so callers report it differently from an
	// upstream error.
	ErrNoActivePiscine = errors.New("no active piscine")

	// ErrNoRaidForAnnouncement is returned when a ready-made announcement needs
	// the current raid (its name) but the piscine has none to talk about.
	ErrNoRaidForAnnouncement = errors.New("no raid available for announcement")
)
