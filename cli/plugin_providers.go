package cli

import (
	"fmt"
	"os"

	"github.com/digitalrebar/provision/client/plugin_providers"
	"github.com/spf13/cobra"
)

type PluginProviderOps struct{ CommonOps }

func (be PluginProviderOps) GetIndexes() map[string]string {
	return map[string]string{}
}

func (be PluginProviderOps) List(parms map[string]string) (interface{}, error) {
	d, e := session.PluginProviders.ListPluginProviders(plugin_providers.NewListPluginProvidersParams(), basicAuth)
	if e != nil {
		return nil, e
	}
	return d.Payload, nil
}

func (be PluginProviderOps) Get(id string) (interface{}, error) {
	d, e := session.PluginProviders.GetPluginProvider(plugin_providers.NewGetPluginProviderParams().WithName(id), basicAuth)
	if e != nil {
		return nil, e
	}
	return d.Payload, nil
}

func (be PluginProviderOps) Upload(name string, f *os.File) (interface{}, error) {
	d, e := session.PluginProviders.UploadPluginProvider(plugin_providers.NewUploadPluginProviderParams().WithName(name).WithBody(f), basicAuth)
	if e != nil {
		return nil, e
	}
	return d.Payload, nil
}

func (be PluginProviderOps) Delete(name string) (interface{}, error) {
	_, e := session.PluginProviders.DeletePluginProvider(plugin_providers.NewDeletePluginProviderParams().WithName(name), basicAuth)
	if e != nil {
		return nil, e
	}
	return "Good", nil
}

func init() {
	tree := addPluginProviderCommands()
	App.AddCommand(tree)
}

func addPluginProviderCommands() (res *cobra.Command) {
	singularName := "plugin_provider"
	name := "plugin_providers"
	d("Making command tree for %v\n", name)
	res = &cobra.Command{
		Use:   name,
		Short: fmt.Sprintf("Access CLI commands relating to %v", name),
	}
	commands := commonOps(&PluginProviderOps{CommonOps{Name: name, SingularName: singularName}})
	res.AddCommand(commands...)
	return res
}
