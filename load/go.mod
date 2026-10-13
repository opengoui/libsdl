module github.com/opengoui/libsdl/load

go 1.27.2

require (
	github.com/opengoui/libsdl v0.0.0
	github.com/opengoui/puresdl v0.0.0
)

require github.com/ebitengine/purego v0.11.1 // indirect

// Development-time replaces: the three modules are developed side by side.
// Remove them (and require tagged versions) when publishing; consumers of
// this module ignore the replace directives of dependencies.
replace github.com/opengoui/libsdl => ../

replace github.com/opengoui/puresdl => ../../puresdl
