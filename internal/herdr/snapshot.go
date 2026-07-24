package herdr

import "context"

// sessionSnapshotResult wraps the session.snapshot payload.
type sessionSnapshotResult struct {
	Type     string   `json:"type"`
	Snapshot Snapshot `json:"snapshot"`
}

// Snapshot fetches the complete topology and agent bootstrap state. It is the
// state engine's source of truth.
func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	var res sessionSnapshotResult
	if err := c.call(ctx, "session.snapshot", struct{}{}, "session_snapshot", &res); err != nil {
		return nil, err
	}
	return &res.Snapshot, nil
}
