package config

// Clone detaches the slices shared by library snapshots.
func (it Item) Clone() Item {
	it.Tags = append([]string(nil), it.Tags...)
	it.Colors = append([]string(nil), it.Colors...)
	return it
}
