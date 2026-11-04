package cli

// SetExportPage makes audit export ask for n entries at a time, and returns
// a function that restores the page size.
func SetExportPage(n int) func() {
	old := exportPage
	exportPage = n
	return func() { exportPage = old }
}
