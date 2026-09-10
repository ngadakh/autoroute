package router

import (
	"fmt"
	"math"
	"sort"

	"github.com/ngadakh/autoroute/internal/embed"
)

// Embedder is the subset of embed.Embedder the classifier needs.
type Embedder interface {
	Embed(text string) ([]float32, error)
}

// Classifier is a nearest-centroid classifier over route exemplars.
type Classifier struct {
	routes    []Route
	centroids [][]float32 // L2-normalised, index-aligned with routes
}

// NewClassifier embeds every exemplar and stores one mean (centroid) vector per
// route. This is the whole "training" step — a few dozen forward passes.
func NewClassifier(e Embedder, routes []Route) (*Classifier, error) {
	c := &Classifier{routes: routes, centroids: make([][]float32, len(routes))}
	for i, r := range routes {
		if len(r.Exemplars) == 0 {
			return nil, fmt.Errorf("route %q has no exemplars", r.Name)
		}
		acc := make([]float32, embed.Dim)
		for _, ex := range r.Exemplars {
			v, err := e.Embed(ex)
			if err != nil {
				return nil, fmt.Errorf("embed exemplar %q: %w", ex, err)
			}
			for d := range acc {
				acc[d] += v[d]
			}
		}
		c.centroids[i] = l2normalise(acc)
	}
	return c, nil
}

// Decision is the L2 classifier's output for one prompt.
type Decision struct {
	Route      string
	Tier       Tier
	Confidence float64 // margin between the top two routes, mapped to [0,1]
	Scores     []RouteScore
}

// RouteScore is the cosine similarity of a prompt to one route centroid.
type RouteScore struct {
	Route string
	Tier  Tier
	Score float64
}

// Classify assigns a prompt to the nearest route centroid. Confidence is the
// normalised gap to the runner-up: a clear winner scores high, a prompt sitting
// between two routes scores low and would be handed to L3 in the real pipeline.
func (c *Classifier) Classify(promptVec []float32) Decision {
	scores := make([]RouteScore, len(c.routes))
	for i, r := range c.routes {
		scores[i] = RouteScore{
			Route: r.Name,
			Tier:  r.Tier,
			Score: float64(embed.Cosine(promptVec, c.centroids[i])),
		}
	}
	sort.Slice(scores, func(a, b int) bool { return scores[a].Score > scores[b].Score })

	top, second := scores[0], scores[1]
	// Map the top-2 margin onto [0,1]. A 0.15 cosine gap is already decisive
	// for MiniLM-scale embeddings, so scale accordingly and clamp.
	conf := math.Min(1, math.Max(0, (top.Score-second.Score)/0.15))

	return Decision{
		Route:      top.Route,
		Tier:       top.Tier,
		Confidence: conf,
		Scores:     scores,
	}
}

func l2normalise(v []float32) []float32 {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm = math.Sqrt(norm); norm > 0 {
		inv := float32(1 / norm)
		for i := range v {
			v[i] *= inv
		}
	}
	return v
}
