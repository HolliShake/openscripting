package main

type Cell struct {
	// Value is the mutable storage shared by local slots and every closure that captures that slot.
	Value OSValue
}
