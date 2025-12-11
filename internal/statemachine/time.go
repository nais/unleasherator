package statemachine

import "time"

// TimeProvider allows mocking time operations for testing
type TimeProvider interface {
	Now() time.Time
	Since(t time.Time) time.Duration
}

// RealTimeProvider implements TimeProvider using real time
type RealTimeProvider struct{}

func (RealTimeProvider) Now() time.Time {
	return time.Now()
}

func (RealTimeProvider) Since(t time.Time) time.Duration {
	return time.Since(t)
}

// MockTimeProvider allows setting specific times for testing
type MockTimeProvider struct {
	CurrentTime time.Time
}

func (m *MockTimeProvider) Now() time.Time {
	return m.CurrentTime
}

func (m *MockTimeProvider) Since(t time.Time) time.Duration {
	return m.CurrentTime.Sub(t)
}

func (m *MockTimeProvider) SetTime(t time.Time) {
	m.CurrentTime = t
}

func (m *MockTimeProvider) AdvanceTime(d time.Duration) {
	m.CurrentTime = m.CurrentTime.Add(d)
}
