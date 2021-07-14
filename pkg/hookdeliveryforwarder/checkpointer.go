package hookdeliveryforwarder

import "time"

type Checkpointer interface {
	GetOrCreate(hookID int64) (*State, error)
	Update(hookID int64, pos *State) error
}

type InMemoryCheckpointer struct {
	t  time.Time
	id int64
}

func (p *InMemoryCheckpointer) GetOrCreate(hookID int64) (*State, error) {
	return &State{DeliveredAt: p.t}, nil
}

func (p *InMemoryCheckpointer) Update(hookID int64, pos *State) error {
	p.t = pos.DeliveredAt
	p.id = pos.ID

	return nil
}

func NewInMemoryLogPositionProvider() Checkpointer {
	return &InMemoryCheckpointer{
		t: time.Now(),
	}
}
