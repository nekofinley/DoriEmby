package source

import "encoding/base64"

// basicAuth builds an HTTP Basic authorization header value.
func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
