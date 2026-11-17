package admin

import (
	"context"
	"time"
)

// UpdateStatus is what the opt-in update check has learned (SPEC §12, ADR 0008).
// With update_check off, kman never asks, and Enabled is false.
type UpdateStatus struct {
	Enabled   bool       `json:"enabled" doc:"Whether update_check is on in kman.yaml. kman asks the GitHub releases API once a day only when it is."`
	Current   string     `json:"current,omitempty" doc:"This binary's version, such as v0.1.0, or empty for a build that is not a release."`
	Latest    string     `json:"latest,omitempty" doc:"The newest release the last successful check found."`
	Available bool       `json:"available" doc:"Whether Latest is newer than Current."`
	URL       string     `json:"url,omitempty" doc:"The page of the latest release."`
	CheckedAt *time.Time `json:"checked_at,omitempty" doc:"When the last check ran, successful or not."`
	Error     string     `json:"error,omitempty" doc:"Why the last check failed, if it did. Latest is still what an earlier check found."`
}

// UpdateStatus says whether a newer release is known. It makes no network
// call: the check runs on its own schedule, in kman serve.
func (s *Service) UpdateStatus(ctx context.Context) (UpdateStatus, error) {
	if _, err := caller(ctx); err != nil {
		return UpdateStatus{}, err
	}
	if s.update == nil {
		return UpdateStatus{}, nil
	}
	return s.update(), nil
}
