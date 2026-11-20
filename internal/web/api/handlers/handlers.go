package handlers

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
)

type Handlers struct {
	ctl     *controllers.Controllers
	version string
}

func New(ctl *controllers.Controllers, version string) *Handlers {
	return &Handlers{ctl: ctl, version: version}
}
