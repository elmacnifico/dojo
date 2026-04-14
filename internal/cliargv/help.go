package cliargv

// HelpRequested reports whether argv contains a help flag as a whole argument
// (-h, -help, --help). This is used before flag.Parse so forms like
// `dojo run --help` still print usage (the stdlib parser stops at the first
// non-flag argument).
func HelpRequested(argv []string) bool {
	for _, a := range argv {
		switch a {
		case "-h", "-help", "--help":
			return true
		default:
		}
	}
	return false
}
