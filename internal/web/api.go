// Package web exposes the API entry point. Future web interfaces live in their own subpackages.
package web

import api "github.com/rmb938/personal-aO3-mirror/internal/web/api"

type API = api.API
type Options = api.Options

const RevealParam = api.RevealParam

func New(opts Options) (*API, error) { return api.New(opts) }
