package tools

func BuiltInDefinitions() []Definition {
	groups := [][]Definition{
		ModelProviderDefinitions(),
		LogDefinitions(),
		SystemDefinitions(),
		AuthKeyDefinitions(),
		ScheduledTaskDefinitions(),
	}
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	definitions := make([]Definition, 0, total)
	for _, group := range groups {
		definitions = append(definitions, group...)
	}
	return definitions
}
