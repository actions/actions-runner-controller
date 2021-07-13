package configmap

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/actions-runner-controller/actions-runner-controller/pkg/hookdeliveryforwarder"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ConfigMapCheckpointer struct {
	Name   string
	NS     string
	Client client.Client
}

type state struct {
	DeliveredAt time.Time `json:"delivered_at"`
	ID          int64     `json:"id"`
}

func (p *ConfigMapCheckpointer) GetOrCreate(hookID int64) (*hookdeliveryforwarder.State, error) {
	var cm corev1.ConfigMap

	if err := p.Client.Get(context.Background(), types.NamespacedName{Namespace: p.NS, Name: p.Name}, &cm); err != nil {
		if !kerrors.IsNotFound(err) {
			return nil, err
		}

		cm.Name = p.Name
		cm.Namespace = p.NS

		if err := p.Client.Create(context.Background(), &cm); err != nil {
			return nil, err
		}
	}

	idStr := fmt.Sprintf("hook_%d", hookID)

	var unmarshalled state

	data, ok := cm.Data[idStr]

	if ok {
		if err := json.Unmarshal([]byte(data), &unmarshalled); err != nil {
			return nil, err
		}
	}

	pos := &hookdeliveryforwarder.State{
		DeliveredAt: unmarshalled.DeliveredAt,
		ID:          unmarshalled.ID,
	}

	if pos.DeliveredAt.IsZero() {
		pos.DeliveredAt = time.Now()
	}

	return pos, nil
}

func (p *ConfigMapCheckpointer) Update(hookID int64, pos *hookdeliveryforwarder.State) error {
	var cm corev1.ConfigMap

	if err := p.Client.Get(context.Background(), types.NamespacedName{Namespace: p.NS, Name: p.Name}, &cm); err != nil {
		return err
	}

	var posData state

	posData.DeliveredAt = pos.DeliveredAt
	posData.ID = pos.ID

	idStr := fmt.Sprintf("hook_%d", hookID)

	data, err := json.Marshal(posData)
	if err != nil {
		return err
	}

	copy := cm.DeepCopy()

	if copy.Data == nil {
		copy.Data = map[string]string{}
	}

	copy.Data[idStr] = string(data)

	if err := p.Client.Patch(context.Background(), copy, client.MergeFrom(&cm)); err != nil {
		return err
	}

	return nil
}
