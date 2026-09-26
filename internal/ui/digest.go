package ui

// shortDigestLength is how much of a digest is shown before it is elided.
const shortDigestLength = 16

// ShortDigest trims a digest or long hash to a readable prefix, leaving short
// values untouched.
func ShortDigest(digest string) string {
	if len(digest) <= shortDigestLength {
		return digest
	}

	return digest[:shortDigestLength] + "..."
}
