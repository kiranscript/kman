package gateway

import . "time"

func elapsed(t Time) Duration { return Since(t) }
