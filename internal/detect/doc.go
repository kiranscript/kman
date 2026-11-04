// Package detect observes events and returns detection decisions: baselines,
// rules and incidents. It decides and never mutates; admin executes its
// decisions as the actor system:detect.
package detect
