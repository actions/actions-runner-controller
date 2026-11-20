package main

import (
	"context"
	"time"

	"github.com/actions/scaleset"
)

// Leave time for a full two-minute failed attempt and another normal long poll,
// while preventing SDK retries from blocking runner reconciliation for ten
// minutes. The deadline includes token refresh and any subsequent poll.
const brokerPollTimeout = 3 * time.Minute

type brokerMessageClient struct {
	*scaleset.MessageSessionClient
}

func (c *brokerMessageClient) GetMessage(ctx context.Context, lastMessageID, maxCapacity int) (*scaleset.RunnerScaleSetMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, brokerPollTimeout)
	defer cancel()
	return c.MessageSessionClient.GetMessage(ctx, lastMessageID, maxCapacity)
}
