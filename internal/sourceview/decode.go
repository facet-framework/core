package sourceview

import "fmt"

// UnmarshalYAML keeps this opt-in block closed even though older project-wide
// configuration permits plugin-specific extensions elsewhere. Personal checkout
// locations belong only in the ignored local bindings file.
func (c *Config) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Config
	var decoded plain
	if err := unmarshal(&decoded); err != nil {
		return errorf("invalid source_views: %v", err)
	}
	var raw map[string]any
	if err := unmarshal(&raw); err != nil {
		return errorf("invalid source_views: %v", err)
	}
	if err := closed(raw, "source_views", "repositories", "registry_repository", "default_view", "views"); err != nil {
		return err
	}
	for name, item := range mapping(raw["repositories"]) {
		if err := closed(mapping(item), "source_views.repositories."+name, "description"); err != nil {
			return err
		}
	}
	for name, item := range mapping(raw["views"]) {
		entry := mapping(item)
		if err := closed(entry, "source_views.views."+name, "sources"); err != nil {
			return err
		}
		for repository, source := range mapping(entry["sources"]) {
			if err := closed(mapping(source), fmt.Sprintf("source_views.views.%s.sources.%s", name, repository), "binding", "ref"); err != nil {
				return err
			}
		}
	}
	*c = Config(decoded)
	return nil
}
func (c *LocalConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain LocalConfig
	var decoded plain
	if err := unmarshal(&decoded); err != nil {
		return errorf("invalid local source bindings: %v", err)
	}
	var raw map[string]any
	if err := unmarshal(&raw); err != nil {
		return errorf("invalid local source bindings: %v", err)
	}
	if err := closed(raw, "local source configuration", "bindings"); err != nil {
		return err
	}
	for name, item := range mapping(raw["bindings"]) {
		if err := closed(mapping(item), "local binding "+name, "repository", "checkout"); err != nil {
			return err
		}
	}
	*c = LocalConfig(decoded)
	return nil
}
func mapping(v any) map[string]any { m, _ := v.(map[string]any); return m }
func closed(fields map[string]any, context string, allowed ...string) error {
	known := map[string]bool{}
	for _, key := range allowed {
		known[key] = true
	}
	for key := range fields {
		if !known[key] {
			return errorf("%s: unknown field %q", context, key)
		}
	}
	return nil
}
