package internal

import (
	"sync"
)

type Session struct {
	sync.Mutex
	Clients               map[byte]*Client
	ClientCount           byte
	MaxClients            byte
	SessionId             uint32
	SyncCount             uint32
	SyncedClients         uint32
	ProposedCountdownTime uint16
	Ready                 bool
}

func NewSession(sessionId uint32, maxClients byte) *Session {
	return &Session{
		Clients:               make(map[byte]*Client, maxClients),
		MaxClients:            maxClients,
		SessionId:             sessionId,
		SyncCount:             1,
		ClientCount:           0,
		SyncedClients:         0,
		Ready:                 false,
		ProposedCountdownTime: 0,
	}
}

func (s *Session) IncrementSyncCount() {
	s.SyncedClients++

	if s.SyncedClients == uint32(s.MaxClients) {
		s.SyncedClients = 0

		for _, client := range s.Clients {
			client.SendSyncResponse()
		}

		s.SyncCount++
	}

	if s.SyncCount > 16 {
		s.SyncCount = 1
	}
}
