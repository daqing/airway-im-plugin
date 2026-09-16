package models

var replModels = map[string]any{}

func registerREPLModel(name string, model any) {
	if name == "" || model == nil {
		return
	}
	replModels[name] = model
}

// REPLModels exposes the plugin's models to the host REPL (lib/plugin's
// REPLModelProvider contract). Returns a copy.
func REPLModels() map[string]any {
	models := make(map[string]any, len(replModels))
	for name, model := range replModels {
		models[name] = model
	}
	return models
}
