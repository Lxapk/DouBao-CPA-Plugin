// Module path matches the repository so the plugin can be imported and built by
// its published URL.
module github.com/Lxapk/DouBao-CPA-Plugin

go 1.26.0

require (
	github.com/router-for-me/CLIProxyAPI/v7 v7.3.15
	gopkg.in/yaml.v3 v3.0.1
)

// Development note
// ---------------
// The SDK is consumed as a published module so that a fresh clone builds
// without any local setup. When working against a checkout of the host, add a
// replace directive locally — but do not commit it: a committed absolute path
// makes the repository unbuildable for everyone else.
//
//	replace github.com/router-for-me/CLIProxyAPI/v7 => ../CLIProxyAPI
