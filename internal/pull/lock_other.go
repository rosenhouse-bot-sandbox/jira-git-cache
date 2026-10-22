//go:build !unix

package pull

import (
	"context"
	"errors"
)

func lock(context.Context, string) (func(), error) {
	return nil, errors.New("pull is not supported on this OS")
}
