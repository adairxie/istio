// Copyright Solo.io, Inc
//
// Licensed under a Solo commercial license, not Apache License, Version 2 or any other variant

package core

import (
	"istio.io/istio/pilot/pkg/features"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/config/host"
	"istio.io/istio/pkg/util/sets"
)

func findWaypointServices(push *model.PushContext) sets.Set[host.Name] {
	if !features.EnableWaypointInterop {
		return nil
	}
	serviceInfos := push.ServicesWithWaypoint("")

	res := sets.New[host.Name]()
	for _, s := range serviceInfos {
		res.Insert(host.Name(s.Service.Hostname))
	}
	return res
}
