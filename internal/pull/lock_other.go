//go:build !unix

package pull

import "errors"

func lock(string) (func(), error) {
	return nil, errors.New("pull is not supported on this OS")
}
