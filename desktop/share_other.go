//go:build !darwin

package main

// enableShareMenu: only the Mac app has a Share menu entry.
func enableShareMenu(string) {}
