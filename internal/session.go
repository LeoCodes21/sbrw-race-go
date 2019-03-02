package internal

import (
	"math"
	"sort"
	"sync"
)

type Session struct {
	sync.Mutex
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
		SyncCount:     1,
		ClientCount:   0,
		SyncedClients: 0,
	}
}

func (s *Session) IncrementSyncCount() {
	s.SyncedClients++

	if s.SyncedClients == uint32(s.MaxClients) {
		s.SyncedClients = 0

		s.SetClientSyncDelays()
		for _, client := range s.Clients {
			client.SendSyncResponse()
		}

		s.SyncCount++
	}
}

func (s *Session) SetClientSyncDelays() {
	if s.ClientCount < 2 {
		return
	}

	s.Lock()

	// Sorting clients by ping in ascending order.
	sortedClients := make([]*Client, len(s.Clients))

	for i, c := range s.Clients {
		sortedClients[i] = c
	}

	sort.SliceStable(sortedClients, func(i, j int) bool {
		return sortedClients[i].Ping < sortedClients[j].Ping
	})

	// The client with the highest ping should be ignored.
	// Calculate sync delays for each client based on the client after.
	for i := 0; i < len(sortedClients) - 1; i++ {
		sortedClients[i].SyncDelay = (sortedClients[i + 1].Ping - sortedClients[i].Ping) + 10
		s.Clients[sortedClients[i].SessionSlot].SyncDelay = sortedClients[i].SyncDelay
	}

	s.Unlock()
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

func (s *Session) GetPingInfluence(c *Client) uint16 {
	var result uint16 = 0

	for _, client := range s.Clients {
		if client.SessionSlot != c.SessionSlot && client.Ping > result {
			result = client.Ping
		}
	}

	return result
}
