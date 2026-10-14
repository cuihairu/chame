package eventing

import (
	"fmt"

	"github.com/cuihairu/chame/core/module"
)

// Bind copies the handlers registered through a module Registry into the
// dispatcher (spec §4.3, §11.4). Handler values are opaque to the
// registry; the typed contract is Handler, so a registration that is not
// one is a configuration error. A type the dispatcher already handles is
// an error too: one consumer path per event type, fan-out is the
// handler's job.
func (d *Dispatcher) Bind(r module.Registry) error {
	for _, eventType := range r.HandlerTypes() {
		v, ok := r.EventHandler(eventType)
		if !ok {
			continue
		}
		h, isHandler := v.(Handler)
		if !isHandler {
			return fmt.Errorf("eventing: handler for %q is not an eventing.Handler", eventType)
		}
		if err := d.Handle(eventType, h); err != nil {
			return fmt.Errorf("eventing: %w", err)
		}
	}
	return nil
}
