package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func downloadFile(client *http.Client, filepath string, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API request at endpoint %s failed with status: %d", url, resp.StatusCode)
	}
	defer resp.Body.Close()

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	return nil
}
