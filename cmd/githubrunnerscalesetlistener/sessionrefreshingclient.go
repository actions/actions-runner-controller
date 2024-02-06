package main

import (
	"context"
	"fmt"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
)

type SessionRefreshingClient struct {
	client  actions.ActionsService
	logger  logr.Logger
	session *actions.RunnerScaleSetSession
}

func newSessionClient(client actions.ActionsService, logger *logr.Logger, session *actions.RunnerScaleSetSession) *SessionRefreshingClient {
	return &SessionRefreshingClient{
		client:  client,
		session: session,
		logger:  logger.WithName("refreshing_client"),
	}
}

func (m *SessionRefreshingClient) GetMessage(ctx context.Context, lastMessageId int64, maxCapacity int) (*actions.RunnerScaleSetMessage, error) {
	if maxCapacity < 0 {
		return nil, fmt.Errorf("maxCapacity must be greater than or equal to 0")
	}

	message, err := m.client.GetMessage(ctx, m.session.MessageQueueUrl, m.session.MessageQueueAccessToken, lastMessageId, maxCapacity)
	if err == nil {
		return message, nil
	}

	expiredError := &actions.MessageQueueTokenExpiredError{}
	if !errors.As(err, &expiredError) {
		return nil, fmt.Errorf("get message failed. %w", err)
	}

	m.logger.Info("message queue token is expired during GetNextMessage, refreshing...")
	session, err := m.client.RefreshMessageSession(ctx, m.session.RunnerScaleSet.Id, m.session.SessionId)
	if err != nil {
		return nil, fmt.Errorf("refresh message session failed. %w", err)
	}

	m.session = session
	message, err = m.client.GetMessage(ctx, m.session.MessageQueueUrl, m.session.MessageQueueAccessToken, lastMessageId, maxCapacity)
	if err != nil {
		return nil, fmt.Errorf("delete message failed after refresh message session. %w", err)
	}

	return message, nil
}

func (m *SessionRefreshingClient) DeleteMessage(ctx context.Context, messageId int64) error {
	err := m.client.DeleteMessage(ctx, m.session.MessageQueueUrl, m.session.MessageQueueAccessToken, messageId)
	if err == nil {
		return nil
	}

	expiredError := &actions.MessageQueueTokenExpiredError{}
	if !errors.As(err, &expiredError) {
		return fmt.Errorf("delete message failed. %w", err)
	}

	m.logger.Info("message queue token is expired during DeleteMessage, refreshing...")
	session, err := m.client.RefreshMessageSession(ctx, m.session.RunnerScaleSet.Id, m.session.SessionId)
	if err != nil {
		return fmt.Errorf("refresh message session failed. %w", err)
	}

	m.session = session
	err = m.client.DeleteMessage(ctx, m.session.MessageQueueUrl, m.session.MessageQueueAccessToken, messageId)
	if err != nil {
		return fmt.Errorf("delete message failed after refresh message session. %w", err)
	}

	return nil

}

func (m *SessionRefreshingClient) AcquireJobs(ctx context.Context, requestIds []int64) ([]int64, error) {
	ids, err := m.client.AcquireJobs(ctx, m.session.RunnerScaleSet.Id, m.session.MessageQueueAccessToken, requestIds)
	if err == nil {
		return ids, nil
	}

	expiredError := &actions.MessageQueueTokenExpiredError{}
	if !errors.As(err, &expiredError) {
		return nil, fmt.Errorf("acquire jobs failed. %w", err)
	}

	m.logger.Info("message queue token is expired during AcquireJobs, refreshing...")
	session, err := m.client.RefreshMessageSession(ctx, m.session.RunnerScaleSet.Id, m.session.SessionId)
	if err != nil {
		return nil, fmt.Errorf("refresh message session failed. %w", err)
	}

	m.session = session
	ids, err = m.client.AcquireJobs(ctx, m.session.RunnerScaleSet.Id, m.session.MessageQueueAccessToken, requestIds)
	if err != nil {
		return nil, fmt.Errorf("acquire jobs failed after refresh message session. %w", err)
	}

	return ids, nil
}

func (m *SessionRefreshingClient) Close() error {
	if m.session == nil {
		m.logger.Info("session is already deleted. (no-op)")
		return nil
	}

	ctxWithTimeout, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	m.logger.Info("deleting session.")
	err := m.client.DeleteMessageSession(ctxWithTimeout, m.session.RunnerScaleSet.Id, m.session.SessionId)
	if err != nil {
		return fmt.Errorf("delete message session failed. %w", err)
	}

	m.session = nil
	return nil
}
