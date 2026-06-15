package printer

import (
	"context"
	"time"
)

const secretStoreTimeout = 10 * time.Second

func secretStoreContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, secretStoreTimeout)
}
