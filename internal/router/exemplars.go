// Package router holds the L2 embedding classifier used by the spike: a set of
// labelled route exemplars, one centroid per route, and nearest-centroid
// assignment with a confidence margin. The real pipeline will layer heuristics
// (L1) and an optional judge (L3) around this; the spike only exercises L2.
package router

// Tier is the model class a prompt is routed to.
type Tier string

const (
	TierCheap    Tier = "cheap"    // Haiku-class: lookups, short factual, simple rewrites
	TierMid      Tier = "mid"      // Sonnet-class: scoped code, structured extraction, summaries
	TierFrontier Tier = "frontier" // Opus/GPT-5-class: proofs, multi-step reasoning, open judgement
)

// Route is a semantic bucket with a target tier and example utterances.
type Route struct {
	Name      string
	Tier      Tier
	Exemplars []string
}

// Routes is the seed taxonomy. Exemplars are intentionally short and varied;
// they are the only "training data" the L2 classifier has in the spike.
var Routes = []Route{
	{
		Name: "factual-lookup", Tier: TierCheap,
		Exemplars: []string{
			"who is the prime minister of india",
			"what is the capital of australia",
			"when did world war two end",
			"how many continents are there",
			"what does HTTP stand for",
			"who wrote pride and prejudice",
		},
	},
	{
		Name: "simple-rewrite", Tier: TierCheap,
		Exemplars: []string{
			"rephrase this sentence to be more polite",
			"fix the grammar in this paragraph",
			"make this email shorter",
			"translate good morning into spanish",
			"convert this list into bullet points",
		},
	},
	{
		Name: "scoped-code-edit", Tier: TierMid,
		Exemplars: []string{
			"add type hints to this function",
			"refactor this loop to a list comprehension",
			"write a unit test for this method",
			"convert this callback code to async await",
			"add error handling to this file read",
			"rename this variable everywhere in the snippet",
		},
	},
	{
		Name: "structured-extraction", Tier: TierMid,
		Exemplars: []string{
			"extract all the dates from this text as json",
			"pull the company names out of this article",
			"summarise this meeting transcript into action items",
			"turn this changelog into a release note table",
		},
	},
	{
		Name: "formal-reasoning", Tier: TierFrontier,
		Exemplars: []string{
			"prove that the sum of the first n odd numbers is n squared",
			"derive the closed form of this recurrence relation",
			"is this argument logically valid and why",
			"work through this probability problem step by step",
			"find the bug in this concurrent algorithm and explain the race",
		},
	},
	{
		Name: "open-judgement", Tier: TierFrontier,
		Exemplars: []string{
			"here is my b2b pricing plan is this a good idea",
			"critique the architecture of this system design",
			"what are the tradeoffs of switching our database now",
			"review this strategy document and tell me what is missing",
			"should we rewrite this service in rust",
		},
	},
}
