package traversal

import (
	"context"
	"fmt"
	"lab-router/internal/model"
	"time"
)

// ProbeSTUN performs a real, temporary binding exchange, then closes the UDP socket.
// It does not enable the production receiver and does not claim a Connection Request succeeded.
func (m *Manager) ProbeSTUN(ctx context.Context, c model.CWMP) (Status, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.runSTUN(child, c) }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	status := func() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.states["stun"] }
	for {
		select {
		case e := <-done:
			v := status()
			if e == nil {
				e = ctx.Err()
			}
			if e == nil && !v.Connected {
				e = fmt.Errorf("nenhum binding confirmado")
			}
			return v, e
		case <-ctx.Done():
			cancel()
			<-done
			return status(), ctx.Err()
		case <-ticker.C:
			v := status()
			if v.Connected {
				cancel()
				<-done
				return v, nil
			}
		}
	}
}
