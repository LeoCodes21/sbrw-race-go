package internal

import (
	"math"
)

func clone(data []byte) []byte {
	newData := make([]byte, len(data))
	copy(newData, data)
	return newData
}

func StdDev(data []uint16) float32 {
	var avg float32 = 0

	for _, d := range data {
		avg += float32(d)
	}

	avg /= float32(len(data))

	var sigma float32 = 0

	for _, d := range data {
		sigma += (float32(d) - avg) * (float32(d) - avg)
	}

	sigma /= float32(len(data))

	return float32(math.Sqrt(float64(sigma)))
}