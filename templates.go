package main

import _ "embed"

// The agent guides shp hands out. They are files so a person can read them,
// and embedded so shp needs nothing outside its binary.

//go:embed templates/worker.md
var workerGuide string

//go:embed templates/reviewer.md
var reviewerGuide string

//go:embed templates/coordinator.md
var coordinatorGuide string
