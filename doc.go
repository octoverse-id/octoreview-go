// Package octoreview is the Go client for Octoreview, the multi-tenant review and rating service
// (https://github.com/octoverse-id/octoreview).
//
// Create a client with [New] or [FromEnv], then call a resource method with a context first:
//
//	client, err := octoreview.FromEnv()
//	review, err := client.Reviews.Create(ctx, octoreview.CreateReviewParams{
//		EntityType: "product", ExternalID: "sku-1", AuthorID: userID, Rating: 5,
//	})
//
// The server's docs/sdk-surface.yaml names every method; docs/openapi.yaml (vendored as JSON in
// testdata/contract) is the contract the offline contract gate checks this package against.
//
// Derive author ids from your own authenticated user, never from request input, and escape
// review text when you render it.
package octoreview
