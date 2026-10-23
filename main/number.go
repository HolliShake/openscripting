package main

func SliceFloat(value float64) (whole int32, decimal int32) {
	whole = int32(value)
	decimal = int32(value*100) % 100
	return whole, decimal
}

func CombineFloat(whole, decimal int32) float64 {
	return float64(whole) + float64(decimal)/100
}
