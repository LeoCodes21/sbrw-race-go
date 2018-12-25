package internal

func clone(data []byte) []byte {
	newData := make([]byte, len(data))
	copy(newData, data)
	return newData
}