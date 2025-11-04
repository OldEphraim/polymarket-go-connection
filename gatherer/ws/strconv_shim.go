package ws

import "strconv"

func strconvParseFloatStd(s string, bitSize int) (float64, error) {
	return strconv.ParseFloat(s, bitSize)
}
