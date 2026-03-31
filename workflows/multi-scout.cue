// Multi-repo scout: spawn parallel scout missions
description: "Spawn parallel scout missions across repos"
start_places: ["request"]
terminal_places: ["done"]

transitions: [
  {
    id:     "spawn-scout"
    in:     ["request"]
    out:    ["scouted"]
    action: "spawn-workflow"
    spawn: {
      template: "scout-mission"
      params: {
        description: "{{description}}"
      }
    }
  },
  {
    id:  "complete"
    in:  ["scouted"]
    out: ["done"]
  },
]
