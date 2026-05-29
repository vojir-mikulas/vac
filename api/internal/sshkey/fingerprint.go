package sshkey

import gossh "golang.org/x/crypto/ssh"

// fingerprintForLine parses an authorized_keys-style line and returns its
// SHA256 fingerprint, or "" if the line can't be parsed.
func fingerprintForLine(line string) string {
	pub, _, _, _, err := gossh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return ""
	}
	return gossh.FingerprintSHA256(pub)
}
