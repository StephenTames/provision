package backend

import (
	"github.com/digitalrebar/provision/backend/index"
	"github.com/digitalrebar/store"
)

// Plugin represents a single instance of a running plugin.
// This contains the configuration need to start this plugin instance.
// swagger:model
type Plugin struct {
	validate

	// The name of the plugin instance.  THis must be unique across all
	// plugins.
	//
	// required: true
	Name string
	// A description of this plugin.  This can contain any reference
	// information for humans you want associated with the plugin.
	Description string
	// Any additional parameters that may be needed to configure
	// the plugin.
	Params map[string]interface{}
	// The plugin provider for this plugin
	//
	// required: true
	Provider string
	// If there are any errors in the start-up process, they will be
	// available here.
	// read only: true
	Errors []string

	p *DataTracker
}

func (n *Plugin) Indexes() map[string]index.Maker {
	fix := AsPlugin
	return map[string]index.Maker{
		"Key": index.MakeKey(),
		"Name": index.Make(
			true,
			"string",
			func(i, j store.KeySaver) bool { return fix(i).Name < fix(j).Name },
			func(ref store.KeySaver) (gte, gt index.Test) {
				refName := fix(ref).Name
				return func(s store.KeySaver) bool {
						return fix(s).Name >= refName
					},
					func(s store.KeySaver) bool {
						return fix(s).Name > refName
					}
			},
			func(s string) (store.KeySaver, error) {
				return &Plugin{Name: s}, nil
			}),
		"Provider": index.Make(
			false,
			"string",
			func(i, j store.KeySaver) bool { return fix(i).Provider < fix(j).Provider },
			func(ref store.KeySaver) (gte, gt index.Test) {
				refProvider := fix(ref).Provider
				return func(s store.KeySaver) bool {
						return fix(s).Provider >= refProvider
					},
					func(s store.KeySaver) bool {
						return fix(s).Provider > refProvider
					}
			},
			func(s string) (store.KeySaver, error) {
				return &Plugin{Provider: s}, nil
			}),
	}
}

func (n *Plugin) Backend() store.Store {
	return n.p.getBackend(n)
}

func (n *Plugin) Prefix() string {
	return "plugins"
}

func (n *Plugin) Key() string {
	return n.Name
}

func (n *Plugin) AuthKey() string {
	return n.Key()
}

func (n *Plugin) GetParams() map[string]interface{} {
	m := n.Params
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

func (n *Plugin) SetParams(d Stores, values map[string]interface{}) error {
	n.Params = values
	e := &Error{Code: 422, Type: ValidationError, o: n}
	_, e2 := n.p.Save(d, n, nil)
	e.Merge(e2)
	return e.OrNil()
}

func (n *Plugin) GetParam(d Stores, key string, searchProfiles bool) (interface{}, bool) {
	mm := n.GetParams()
	if v, found := mm[key]; found {
		return v, true
	}
	return nil, false
}

func (n *Plugin) New() store.KeySaver {
	res := &Plugin{Name: n.Name, p: n.p}
	return store.KeySaver(res)
}

func (n *Plugin) setDT(p *DataTracker) {
	n.p = p
}

func (n *Plugin) Validate() error {
	return index.CheckUnique(n, n.stores("plugins").Items())
}

func (n *Plugin) BeforeSave() error {
	e := &Error{Code: 422, Type: ValidationError, o: n}
	if err := n.Validate(); err != nil {
		e.Merge(err)
	}
	if n.Provider == "" {
		e.Errorf("Plugin %s must have a provider", n.Name)
	}
	return e.OrNil()
}

func (p *DataTracker) NewPlugin() *Plugin {
	return &Plugin{p: p}
}

func AsPlugin(o store.KeySaver) *Plugin {
	return o.(*Plugin)
}

func AsPlugins(o []store.KeySaver) []*Plugin {
	res := make([]*Plugin, len(o))
	for i := range o {
		res[i] = AsPlugin(o[i])
	}
	return res
}

var pluginLockMap = map[string][]string{
	"get":    []string{"plugins", "params"},
	"create": []string{"plugins", "params"},
	"update": []string{"plugins", "params"},
	"patch":  []string{"plugins", "params"},
	"delete": []string{"plugins", "params"},
}

func (m *Plugin) Locks(action string) []string {
	return pluginLockMap[action]
}
