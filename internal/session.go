package internal

import "math"

type Session struct {
	Clients       map[byte]*Client
	ClientCount   byte
	MaxClients    byte
	SessionId     uint32
	SyncCount     uint32
	SyncedClients uint32
}

func NewSession(sessionId uint32, maxClients byte) *Session {
	return &Session{
		Clients:       make(map[byte]*Client, maxClients),
		MaxClients:    maxClients,
		SessionId:     sessionId,
		SyncCount:     0,
		ClientCount:   0,
		SyncedClients: 0,
	}
}

func (s *Session) IncrementSyncCount() {
	s.SyncedClients++

	if s.SyncedClients == uint32(s.MaxClients) {
		s.SyncedClients = 0
		s.SyncCount++

		for _, client := range s.Clients {
			client.SendSyncResponse()
		}
	}
}

func (s *Session) IsAllPlayerInfoBeforeOk() bool {
	for _, client := range s.Clients {
		if !client.IsPlayerInfoBeforeOk() {
			return false
		}
	}

	return true
}

func (s *Session) GetHelloTimeDeviation() uint16 {
	data := make([]uint16, s.ClientCount)
	dataIdx := 0

	for _, client := range s.Clients {
		data[dataIdx] = client.CliHelloTime
	}

	return uint16(math.Round(float64(StdDev(data))))
}

func (s *Session) GetPingDeviation() uint16 {
	data := make([]uint16, s.ClientCount)
	dataIdx := 0

	for _, client := range s.Clients {
		data[dataIdx] = client.Ping
	}

	return uint16(math.Round(float64(StdDev(data))))
}