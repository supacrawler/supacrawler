package worker

import (
	"context"

	"github.com/hibiken/asynq"
)

type Mux struct{ mux *asynq.ServeMux }

func NewMux() *Mux { return &Mux{mux: asynq.NewServeMux()} }

func (m *Mux) HandleFunc(t string, h func(ctx context.Context, task *asynq.Task) error) {
	m.mux.HandleFunc(t, h)
}

func (m *Mux) Mux() *asynq.ServeMux { return m.mux }
