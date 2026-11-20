package handlers

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

type HandlerFunc = runtime.HandlerFunc
type Response = runtime.Response
type Page = runtime.Page

const MaxID = runtime.MaxID

var (
	ParseParams            = runtime.ParseParams
	OnlyPathID             = runtime.OnlyPathID
	NoParams               = runtime.NoParams
	NoContent              = runtime.NoContent
	detailConfirmAdminOnly = runtime.DetailConfirmAdminOnly
	detailJobState         = runtime.DetailJobState
	detailJobKind          = runtime.DetailJobKind
	detailID               = runtime.DetailID
)

type Handlers struct {
	ctl     *controllers.Controllers
	version string
}

func New(ctl *controllers.Controllers, version string) *Handlers {
	return &Handlers{ctl: ctl, version: version}
}
