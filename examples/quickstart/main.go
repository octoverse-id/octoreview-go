// Command quickstart creates a review with a photo and prints its id, the entity's rating
// summary and the photo URL (server docs/spec.md section 18).
//
// Run the server's compose stack and copy its demo settings out first:
//
//	make up && make dev-env            # in the octoreview server repository
//	go run ./examples/quickstart path/to/.octoreview-dev.env
package main

import (
	"bufio"
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	octoreview "github.com/octoverse-id/octoreview-go"
)

//go:embed photo.png
var photo []byte

func main() {
	envFile := ".octoreview-dev.env"
	if len(os.Args) > 1 {
		envFile = os.Args[1]
	}
	if err := loadEnvFile(envFile); err != nil {
		log.Fatalf("quickstart: %v (run `make dev-env` in the server repository)", err)
	}
	client, err := octoreview.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	review, err := client.Reviews.Create(ctx, octoreview.CreateReviewParams{
		EntityType: "product",
		ExternalID: "sku-quickstart",
		// In your application, derive this from your own authenticated user.
		AuthorID: fmt.Sprintf("quickstart-go-%d", time.Now().UnixNano()),
		Rating:   5,
		Title:    octoreview.Ptr("Fits perfectly"),
		Body:     octoreview.Ptr("Sent with the octoreview Go quickstart."),
	})
	if err != nil {
		log.Fatal(err)
	}
	media, err := client.Media.UploadBytes(ctx, review.ID, photo, "image/png")
	if err != nil {
		log.Fatal(err)
	}
	summary, err := client.Ratings.Get(ctx, "product", "sku-quickstart")
	if err != nil {
		log.Fatal(err)
	}
	average := "none"
	if summary.RatingAverage != nil {
		average = fmt.Sprintf("%.2f", *summary.RatingAverage)
	}
	url := "(no URL)"
	if media.URL != nil {
		url = *media.URL
	}
	fmt.Println("review:", review.ID)
	fmt.Printf("rating: average %s from %d published review(s)\n", average, summary.RatingCount)
	fmt.Println("photo:", url)
}

// loadEnvFile sets KEY=VALUE lines that are not already set in the environment.
func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		if _, set := os.LookupEnv(key); !set {
			_ = os.Setenv(key, value)
		}
	}
	return scanner.Err()
}
