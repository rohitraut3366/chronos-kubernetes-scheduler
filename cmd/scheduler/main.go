/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"os"

	"fastest-empty-node-scheduler/internal/scheduler"

	"k8s.io/klog/v2"
	"k8s.io/kubernetes/cmd/kube-scheduler/app"
)

// main is the entry point for the scheduler.
func main() {
	command := app.NewSchedulerCommand(
		app.WithPlugin(scheduler.PluginName, scheduler.New),
	)

	if err := command.Execute(); err != nil {
		klog.Fatalf("Error executing scheduler command: %v", err)
		os.Exit(1)
	}
}
