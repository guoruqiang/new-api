package authz

const (
	ResourceRisk  = "risk"
	ActionUnblock = "unblock"
)

var (
	RiskRead    = Permission{Resource: ResourceRisk, Action: ActionRead}
	RiskWrite   = Permission{Resource: ResourceRisk, Action: ActionWrite}
	RiskUnblock = Permission{Resource: ResourceRisk, Action: ActionUnblock}
)

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceRisk,
		LabelKey: "Risk Audit",
		Actions: []ActionDefinition{
			{
				Action:         ActionRead,
				LabelKey:       "View risk audit",
				DescriptionKey: "View risk statistics, events, refusal records, subject blocks and configuration without provider keys.",
			},
			{
				Action:         ActionWrite,
				LabelKey:       "Configure risk audit",
				DescriptionKey: "Publish risk configuration and policies, edit decision model providers and clear refusal records.",
			},
			{
				Action:         ActionUnblock,
				LabelKey:       "Release risk blocks",
				DescriptionKey: "Release subjects blocked by the risk audit.",
			},
		},
	})
}
