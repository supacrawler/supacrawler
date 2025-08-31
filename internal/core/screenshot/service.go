package screenshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scraper/internal/config"
	"scraper/internal/core/job"
	"scraper/internal/logger"
	"scraper/internal/platform/engineapi"
	tasks "scraper/internal/platform/tasks"

	"github.com/antoineross/supabase-go"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/hibiken/asynq"
	"github.com/playwright-community/playwright-go"
	storage_go "github.com/supabase-community/storage-go"
)

type Service struct {
	log  *logger.Logger
	cfg  config.Config
	jobs *job.JobService

	supabaseClient *supabase.Client
}

// Use the generated OpenAPI type instead of custom struct
type Request = engineapi.ScreenshotCreateRequest

type Payload struct {
	JobID   string  `json:"job_id"`
	UserID  string  `json:"user_id,omitempty"`
	Request Request `json:"request"`
}

const TaskTypeScreenshot = "screenshot:task"

func New(cfg config.Config, jobs *job.JobService) (*Service, error) {
	s := &Service{log: logger.New("ScreenshotService"), cfg: cfg, jobs: jobs}

	// In production, Supabase configuration is required
	if cfg.AppEnv == "production" {
		if cfg.SupabaseURL == "" || cfg.SupabaseServiceKey == "" || cfg.SupabaseBucket == "" {
			return nil, fmt.Errorf("production environment requires Supabase configuration: NEXT_PUBLIC_SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY, and SUPABASE_STORAGE_BUCKET must be set")
		}
	}

	// Initialize supabase client if credentials provided
	if cfg.SupabaseURL != "" && cfg.SupabaseServiceKey != "" {
		client, err := supabase.NewClient(cfg.SupabaseURL, cfg.SupabaseServiceKey, nil)
		if err != nil {
			if cfg.AppEnv == "production" {
				return nil, fmt.Errorf("failed to initialize Supabase client in production: %w", err)
			}
			s.log.LogWarnf("failed to initialize Supabase client: %v", err)
		} else {
			s.supabaseClient = client
		}
	}
	return s, nil
}

func (s *Service) Enqueue(ctx context.Context, t *tasks.Client, req Request) (string, error) {
	jobID := utils.UUIDv4()
	payload, _ := json.Marshal(Payload{JobID: jobID, Request: req})
	if err := s.jobs.InitPending(ctx, jobID, job.TypeScreenshot, req.Url); err != nil {
		return "", err
	}
	task := asynq.NewTask(TaskTypeScreenshot, payload)
	if err := t.Enqueue(task, "default", 10); err != nil {
		return "", err
	}
	return jobID, nil
}

func (s *Service) HandleTask(ctx context.Context, task *asynq.Task) error {
	var p Payload
	if err := json.Unmarshal(task.Payload(), &p); err != nil {
		return err
	}
	if err := s.jobs.SetProcessing(ctx, p.JobID, job.TypeScreenshot); err != nil {
		return err
	}
	// Force async save regardless of Stream setting for background tasks
	req := p.Request
	req.Stream = &[]bool{false}[0] // Create pointer to false
	res, err := s.take(ctx, req)
	if err != nil {
		return s.jobs.Complete(ctx, p.JobID, job.TypeScreenshot, job.StatusFailed, nil)
	}
	jr := job.ScreenshotResult{URL: p.Request.Url, Path: res.Path, PublicURL: res.PublicURL, Metadata: res.Metadata}
	return s.jobs.Complete(ctx, p.JobID, job.TypeScreenshot, job.StatusCompleted, jr)
}

type Result struct {
	Path      string
	PublicURL string
	Metadata  engineapi.ScreenshotMetadata
}

func (s *Service) take(_ context.Context, r Request) (Result, error) {
	// Use service helper methods for pointer dereferencing
	// Set environment variable to prevent font loading delays
	os.Setenv("PW_TEST_SCREENSHOT_NO_FONTS_READY", "1")
	defer os.Unsetenv("PW_TEST_SCREENSHOT_NO_FONTS_READY")

	pw, err := playwright.Run()
	if err != nil {
		s.log.LogErrorf("Failed to start Playwright: %v", err)
		return Result{}, fmt.Errorf("playwright initialization failed: %w", err)
	}
	defer pw.Stop()

	// Build browser launch arguments
	args := []string{
		"--no-sandbox",
		"--disable-dev-shm-usage", // Overcome limited resource problems
		"--disable-gpu",
		"--disable-web-security",
		"--disable-features=VizDisplayCompositor",
	}

	// Add additional args based on request options
	if s.getBool(r.IgnoreHttps, false) {
		args = append(args, "--ignore-certificate-errors", "--ignore-ssl-errors")
	}
	if s.getBool(r.DisableJs, false) {
		args = append(args, "--disable-javascript")
	}

	// Launch browser with optimized settings
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Args:     args,
	})
	if err != nil {
		s.log.LogErrorf("Failed to launch browser: %v", err)
		return Result{}, fmt.Errorf("browser launch failed: %w", err)
	}
	defer browser.Close()

	// Configure device settings and context options
	contextOptions := playwright.BrowserNewContextOptions{}

	// Set device configuration
	device := s.getString((*string)(r.Device), "desktop")
	switch device {
	case "mobile":
		contextOptions.Viewport = &playwright.Size{Width: 375, Height: 667}
		contextOptions.DeviceScaleFactor = playwright.Float(2.0)
		contextOptions.IsMobile = playwright.Bool(true)
		contextOptions.HasTouch = playwright.Bool(true)
		if s.getString(r.UserAgent, "") == "" {
			contextOptions.UserAgent = playwright.String("Mozilla/5.0 (iPhone; CPU iPhone OS 14_7_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.1.2 Mobile/15E148 Safari/604.1")
		}
	case "tablet":
		if s.getBool(r.IsLandscape, false) {
			contextOptions.Viewport = &playwright.Size{Width: 1024, Height: 768}
		} else {
			contextOptions.Viewport = &playwright.Size{Width: 768, Height: 1024}
		}
		contextOptions.DeviceScaleFactor = playwright.Float(2.0)
		contextOptions.IsMobile = playwright.Bool(true)
		contextOptions.HasTouch = playwright.Bool(true)
		if s.getString(r.UserAgent, "") == "" {
			contextOptions.UserAgent = playwright.String("Mozilla/5.0 (iPad; CPU OS 14_7_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.1.2 Mobile/15E148 Safari/604.1")
		}
	case "desktop":
		contextOptions.Viewport = &playwright.Size{Width: 1920, Height: 1080}
		contextOptions.DeviceScaleFactor = playwright.Float(1.0)
		contextOptions.IsMobile = playwright.Bool(false)
		contextOptions.HasTouch = playwright.Bool(false)
	case "custom":
		width := s.getInt(r.Width, 1920)
		height := s.getInt(r.Height, 1080)
		if width > 0 && height > 0 {
			contextOptions.Viewport = &playwright.Size{Width: width, Height: height}
		}
		if s.getFloat32(r.DeviceScale, 0) > 0 {
			contextOptions.DeviceScaleFactor = playwright.Float(float64(s.getFloat32(r.DeviceScale, 1.0)))
		}
		contextOptions.IsMobile = playwright.Bool(s.getBool(r.IsMobile, false))
		contextOptions.HasTouch = playwright.Bool(s.getBool(r.HasTouch, false))
	default:
		// Default to desktop
		contextOptions.Viewport = &playwright.Size{Width: 1920, Height: 1080}
		contextOptions.DeviceScaleFactor = playwright.Float(1.0)
	}

	// Apply accessibility and visual settings
	if s.getBool(r.DarkMode, false) || s.getBool(r.ReducedMotion, false) || s.getBool(r.HighContrast, false) {
		if s.getBool(r.DarkMode, false) {
			contextOptions.ColorScheme = playwright.ColorSchemeDark
		} else {
			contextOptions.ColorScheme = playwright.ColorSchemeLight
		}

		if s.getBool(r.ReducedMotion, false) {
			contextOptions.ReducedMotion = playwright.ReducedMotionReduce
		}

		// Force media for accessibility
		if s.getBool(r.HighContrast, false) {
			contextOptions.ForcedColors = playwright.ForcedColorsActive
		} else {
			contextOptions.ForcedColors = playwright.ForcedColorsNone
		}
	}

	// Set custom user agent if provided
	if s.getString(r.UserAgent, "") != "" {
		contextOptions.UserAgent = playwright.String(s.getString(r.UserAgent, ""))
	}

	// Set custom headers if provided
	headers := s.getStringMap(r.Headers, nil)
	if len(headers) > 0 {
		contextOptions.ExtraHttpHeaders = headers
	}

	// Ignore HTTPS errors if requested
	if s.getBool(r.IgnoreHttps, false) {
		contextOptions.IgnoreHttpsErrors = playwright.Bool(true)
	}

	// Set print mode if requested
	if s.getBool(r.PrintMode, false) {
		contextOptions.ColorScheme = playwright.ColorSchemeNoPreference
	}

	// Create browser context with all options
	ctx, err := browser.NewContext(contextOptions)
	if err != nil {
		s.log.LogErrorf("Failed to create browser context: %v", err)
		return Result{}, fmt.Errorf("browser context creation failed: %w", err)
	}
	defer ctx.Close()

	// Set up resource blocking if needed
	var blockResources []string
	if r.BlockResources != nil {
		for _, resource := range *r.BlockResources {
			blockResources = append(blockResources, string(resource))
		}
	}
	if s.getBool(r.BlockAds, false) || s.getBool(r.BlockCookies, false) || s.getBool(r.BlockChats, false) || s.getBool(r.BlockTrackers, false) || len(blockResources) > 0 {
		if err := ctx.Route("**/*", func(route playwright.Route) {
			url := route.Request().URL()
			resourceType := route.Request().ResourceType()

			// Block based on URL patterns
			if s.getBool(r.BlockAds, false) && s.isAdUrl(url) {
				route.Abort("blockedbyclient")
				return
			}
			if s.getBool(r.BlockCookies, false) && s.isCookieUrl(url) {
				route.Abort("blockedbyclient")
				return
			}
			if s.getBool(r.BlockChats, false) && s.isChatUrl(url) {
				route.Abort("blockedbyclient")
				return
			}
			if s.getBool(r.BlockTrackers, false) && s.isTrackerUrl(url) {
				route.Abort("blockedbyclient")
				return
			}

			// Block based on resource types
			for _, blocked := range blockResources {
				if string(resourceType) == blocked {
					route.Abort("blockedbyclient")
					return
				}
			}

			route.Continue()
		}); err != nil {
			s.log.LogWarnf("Failed to set up resource blocking: %v", err)
		}
	}

	page, err := ctx.NewPage()
	if err != nil {
		s.log.LogErrorf("Failed to create page: %v", err)
		return Result{}, fmt.Errorf("page creation failed: %w", err)
	}

	// Set cookies if provided
	cookiesData := s.getCookies(r.Cookies, nil)
	if len(cookiesData) > 0 {
		cookies := make([]playwright.OptionalCookie, 0, len(cookiesData))
		for _, cookieMap := range cookiesData {
			if name, ok := cookieMap["name"].(string); ok {
				if value, ok := cookieMap["value"].(string); ok {
					cookie := playwright.OptionalCookie{
						Name:  name,
						Value: value,
					}
					if domain, ok := cookieMap["domain"].(string); ok {
						cookie.Domain = &domain
					}
					if path, ok := cookieMap["path"].(string); ok {
						cookie.Path = &path
					}
					cookies = append(cookies, cookie)
				}
			}
		}
		if len(cookies) > 0 {
			if err := ctx.AddCookies(cookies); err != nil {
				s.log.LogWarnf("Failed to set cookies: %v", err)
			}
		}
	}

	// JavaScript disabled at browser level if r.DisableJS is true
	// No additional action needed here

	// Set custom viewport if specified (override device defaults)
	if s.getString((*string)(r.Device), "") == "custom" && s.getInt(r.Width, 0) > 0 && s.getInt(r.Height, 0) > 0 {
		width := s.getInt(r.Width, 0)
		height := s.getInt(r.Height, 0)
		if err := page.SetViewportSize(width, height); err != nil {
			s.log.LogWarnf("Failed to set viewport size %dx%d: %v", width, height, err)
		}
	}

	// Configure navigation options based on request parameters
	url := r.Url
	s.log.LogDebugf("Navigating to URL: %s", url)

	// Determine wait condition
	waitUntil := playwright.WaitUntilStateDomcontentloaded // Default
	waitUntilStr := s.getString((*string)(r.WaitUntil), "domcontentloaded")
	switch waitUntilStr {
	case "load":
		waitUntil = playwright.WaitUntilStateLoad
	case "domcontentloaded":
		waitUntil = playwright.WaitUntilStateDomcontentloaded
	case "networkidle":
		waitUntil = playwright.WaitUntilStateNetworkidle
	}

	// Set timeout (default 30s, max from request or 30s)
	timeout := 30000.0 // 30 seconds default
	timeoutSeconds := s.getInt(r.Timeout, 0)
	if timeoutSeconds > 0 {
		timeout = float64(timeoutSeconds * 1000) // Convert seconds to milliseconds
	}

	gotoOptions := playwright.PageGotoOptions{
		WaitUntil: waitUntil,
		Timeout:   playwright.Float(timeout),
	}

	if _, err := page.Goto(url, gotoOptions); err != nil {
		s.log.LogErrorf("Failed to navigate to %s: %v", url, err)
		if strings.Contains(err.Error(), "timeout") {
			return Result{}, fmt.Errorf("page load timeout: %w", err)
		}
		if strings.Contains(err.Error(), "net::") {
			return Result{}, fmt.Errorf("network error accessing page: %w", err)
		}
		return Result{}, fmt.Errorf("navigation failed: %w", err)
	}

	// Wait for specific selector if requested
	waitSelector := s.getString(r.WaitForSelector, "")
	if waitSelector != "" {
		s.log.LogDebugf("Waiting for selector: %s", waitSelector)
		if err := page.Locator(waitSelector).WaitFor(playwright.LocatorWaitForOptions{
			Timeout: playwright.Float(10000), // 10 second timeout for selector
		}); err != nil {
			s.log.LogWarnf("Selector wait failed: %v", err)
		}
	}

	// Click element if requested
	clickSelector := s.getString(r.ClickSelector, "")
	if clickSelector != "" {
		s.log.LogDebugf("Clicking selector: %s", clickSelector)
		if err := page.Locator(clickSelector).Click(); err != nil {
			s.log.LogWarnf("Click failed: %v", err)
		}
		// Wait a moment after clicking using page.WaitForLoadState
		page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State:   playwright.LoadStateNetworkidle,
			Timeout: playwright.Float(3000),
		})
	}

	// Hide elements if requested
	hideSelectors := s.getStringSlice(r.HideSelectors, nil)
	if len(hideSelectors) > 0 {
		for _, selector := range hideSelectors {
			s.log.LogDebugf("Hiding selector: %s", selector)
			if _, err := page.EvaluateHandle(fmt.Sprintf(`
				document.querySelectorAll('%s').forEach(el => el.style.display = 'none')
			`, selector)); err != nil {
				s.log.LogWarnf("Failed to hide selector %s: %v", selector, err)
			}
		}
	}

	// Additional delay if requested
	delay := s.getInt(r.Delay, 0)
	if delay > 0 {
		s.log.LogDebugf("Additional delay: %d seconds", delay)
		// Use WaitForLoadState with networkidle instead of arbitrary timeout
		page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State:   playwright.LoadStateNetworkidle,
			Timeout: playwright.Float(float64(delay * 1000)),
		})
	} else {
		// Default wait for dynamic content - wait for network to be idle
		page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State:   playwright.LoadStateNetworkidle,
			Timeout: playwright.Float(5000), // 5 second max wait for network idle
		})
	}

	// Configure screenshot options
	// Use the same timeout as navigation, or default to 30 seconds
	screenshotTimeout := timeout
	if screenshotTimeout == 0 {
		screenshotTimeout = 30000.0 // 30 seconds default
	}

	opts := playwright.PageScreenshotOptions{
		FullPage: playwright.Bool(s.getBool(r.FullPage, false)),
		Timeout:  playwright.Float(screenshotTimeout), // Use request timeout or 30s default
	}

	// Set image format and quality
	format := s.getString((*string)(r.Format), "png")
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		opts.Type = playwright.ScreenshotTypeJpeg
		quality := s.getInt(r.Quality, 85)
		if quality > 0 && quality <= 100 {
			opts.Quality = &quality
		} else {
			// Default to 85% quality for JPEG
			defaultQuality := 85
			opts.Quality = &defaultQuality
		}
	case "webp":
		// Note: WebP support might not be available in all Playwright versions
		// Fallback to PNG for now
		opts.Type = playwright.ScreenshotTypePng
	default:
		opts.Type = playwright.ScreenshotTypePng
	}

	s.log.LogDebugf("Taking screenshot with format %s, fullpage: %v", format, s.getBool(r.FullPage, false))
	start := time.Now()
	buf, err := page.Screenshot(opts)
	if err != nil {
		s.log.LogErrorf("Failed to capture screenshot: %v", err)
		if strings.Contains(err.Error(), "timeout") {
			return Result{}, fmt.Errorf("screenshot capture timeout: %w", err)
		}
		return Result{}, fmt.Errorf("screenshot capture failed: %w", err)
	}

	// Validate screenshot buffer
	if len(buf) == 0 {
		s.log.LogError("Screenshot buffer is empty", nil)
		return Result{}, fmt.Errorf("screenshot capture resulted in empty image")
	}

	// Check for reasonable file size limits (warn if over 10MB)
	fileSize := len(buf)
	if fileSize > 10*1024*1024 {
		s.log.LogWarnf("Large screenshot file size: %d bytes for URL %s", fileSize, url)
	}

	width := s.getInt(r.Width, 1920)
	height := s.getInt(r.Height, 1080)
	formatStr := strings.ToLower(string(*opts.Type))
	load := int(time.Since(start).Milliseconds())

	meta := engineapi.ScreenshotMetadata{
		Width:    &width,
		Height:   &height,
		Format:   &formatStr,
		FileSize: &fileSize,
		LoadTime: &load,
	}

	s.log.LogDebugf("Screenshot captured: %dx%d, %s, %d bytes, %dms", width, height, formatStr, fileSize, load)

	path, public, err := s.save(buf, r)
	if err != nil {
		s.log.LogErrorf("Failed to save screenshot: %v", err)
		return Result{}, fmt.Errorf("screenshot save failed: %w", err)
	}

	s.log.LogInfof("Screenshot completed successfully for %s: %s", url, public)
	return Result{Path: path, PublicURL: public, Metadata: meta}, nil
}

// Helper methods for safely dereferencing pointers
func (s *Service) getBool(ptr *bool, def bool) bool {
	if ptr == nil {
		return def
	}
	return *ptr
}

func (s *Service) getInt(ptr *int, def int) int {
	if ptr == nil {
		return def
	}
	return *ptr
}

func (s *Service) getString(ptr *string, def string) string {
	if ptr == nil {
		return def
	}
	return *ptr
}

func (s *Service) getFloat32(ptr *float32, def float32) float32 {
	if ptr == nil {
		return def
	}
	return *ptr
}

func (s *Service) getStringSlice(ptr *[]string, def []string) []string {
	if ptr == nil {
		return def
	}
	return *ptr
}

func (s *Service) getStringMap(ptr *map[string]string, def map[string]string) map[string]string {
	if ptr == nil {
		return def
	}
	return *ptr
}

func (s *Service) getCookies(ptr *[]map[string]interface{}, def []map[string]interface{}) []map[string]interface{} {
	if ptr == nil {
		return def
	}
	return *ptr
}

func (s *Service) save(data []byte, r Request) (string, string, error) {
	// Debug: Check Supabase configuration
	s.log.LogInfof("🔍 Supabase config check:")
	s.log.LogInfof("  - Client initialized: %v", s.supabaseClient != nil)
	s.log.LogInfof("  - Bucket: '%s'", s.cfg.SupabaseBucket)
	s.log.LogInfof("  - URL set: %v", s.cfg.SupabaseURL != "")
	s.log.LogInfof("  - ServiceKey set: %v", s.cfg.SupabaseServiceKey != "")
	s.log.LogInfof("  - App Environment: %s", s.cfg.AppEnv)

	// If supabase configured, upload to bucket and return signed URL
	if s.supabaseClient != nil && s.cfg.SupabaseBucket != "" && s.cfg.SupabaseURL != "" && s.cfg.SupabaseServiceKey != "" {
		s.log.LogDebugf("Attempting Supabase upload...")
		name := time.Now().Format("20060102_150405") + "_" + sanitize(r.Url) + "." + strings.ToLower(s.getString((*string)(r.Format), "png"))
		bucketPath := filepath.ToSlash(filepath.Join("screenshots", name))

		// Determine mime type from filename extension
		mimeType := mime.TypeByExtension(filepath.Ext(bucketPath))
		if mimeType == "" {
			format := s.getString((*string)(r.Format), "png")
			if strings.EqualFold(format, "jpeg") || strings.EqualFold(format, "jpg") {
				mimeType = "image/jpeg"
			} else {
				mimeType = "image/png"
			}
		}

		reader := bytes.NewReader(data)
		if _, err := s.supabaseClient.Storage.UploadFile(s.cfg.SupabaseBucket, bucketPath, reader, storage_go.FileOptions{ContentType: &mimeType}); err != nil {
			s.log.LogWarnf("Supabase upload failed: %v", err)
			// In production, return error instead of falling back to local storage
			if s.cfg.AppEnv == "production" {
				return "", "", fmt.Errorf("failed to upload screenshot to Supabase storage in production: %w", err)
			}
			// Fallback to local file on upload failure for non-production
			goto LOCAL
		}
		s.log.LogDebugf("Supabase upload successful, creating signed URL...")

		// Create signed URL - you can switch between methods here
		signed, err := s.createSignedURLWorkaround(s.cfg.SupabaseBucket, bucketPath, 15*60)

		if err != nil {
			s.log.LogWarnf("Supabase signed URL creation failed: %v", err)
			// In production, return error instead of falling back to local storage
			if s.cfg.AppEnv == "production" {
				return "", "", fmt.Errorf("failed to create signed URL for screenshot in production: %w", err)
			}
			// Fallback to local file on sign failure for non-production
			goto LOCAL
		}
		s.log.LogInfof("Successfully created Supabase signed URL: %s", signed)
		return "", signed, nil
	} else {
		s.log.LogWarnf("Supabase not configured properly, using local storage fallback")
	}

	// In production, if Supabase is not configured, return error
	if s.cfg.AppEnv == "production" {
		return "", "", fmt.Errorf("supabase storage is required in production environment")
	}

LOCAL:
	// Local-only fallback (only allowed in non-production environments)
	_ = os.MkdirAll(filepath.Join(s.cfg.DataDir, "screenshots"), 0o755)
	name := time.Now().Format("20060102_150405") + "_" + sanitize(r.Url) + ".png"
	path := filepath.Join(s.cfg.DataDir, "screenshots", name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", "", err
	}
	return path, "/files/screenshots/" + name, nil
}

// createSignedURLWorkaround performs a direct REST call to sign objects with fresh headers
func (s *Service) createSignedURLWorkaround(bucket string, objectPath string, expiresIn int) (string, error) {
	if s.cfg.SupabaseURL == "" {
		return "", fmt.Errorf("supabase URL not configured")
	}
	serviceKey := s.cfg.SupabaseServiceKey
	if serviceKey == "" {
		return "", fmt.Errorf("supabase service key not configured")
	}

	signURL := fmt.Sprintf("%s/storage/v1/object/sign/%s/%s", strings.TrimRight(s.cfg.SupabaseURL, "/"), bucket, objectPath)
	body := map[string]int{"expiresIn": expiresIn}
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		return "", fmt.Errorf("failed to encode sign body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, signURL, buf)
	if err != nil {
		return "", fmt.Errorf("failed to build sign request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	req.Header.Set("apikey", serviceKey)

	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request signed URL: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("failed to create signed URL: status %d", resp.StatusCode)
	}

	var signed struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&signed); err != nil {
		return "", fmt.Errorf("failed to decode signed URL response: %w", err)
	}

	base := strings.TrimRight(s.cfg.SupabaseURL, "/")
	path := signed.SignedURL
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasPrefix(path, "/storage/v1/") {
		path = "/storage/v1" + path
	}
	finalURL := base + path
	if s.cfg.AppEnv == "local" || s.cfg.AppEnv == "development" {
		finalURL = strings.Replace(finalURL, "host.docker.internal", "127.0.0.1", 1)
	}

	s.log.LogDebugf("Workaround returned signed URL: %s", finalURL)
	return finalURL, nil
}

func sanitize(u string) string {
	replacer := strings.NewReplacer(":", "-", "/", "-", "?", "-", "&", "-", "=", "-", "#", "-", "%", "")
	out := replacer.Replace(u)
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// Helper functions for URL blocking
func (s *Service) isAdUrl(url string) bool {
	adPatterns := []string{
		"googlesyndication.com", "doubleclick.net", "googleadservices.com", "googletag",
		"amazon-adsystem.com", "facebook.com/plugins", "fbcdn.net", "outbrain.com",
		"taboola.com", "adsystem.amazon", "googleads", "/ads/", "/ad?", "adsense",
	}
	for _, pattern := range adPatterns {
		if strings.Contains(url, pattern) {
			return true
		}
	}
	return false
}

func (s *Service) isCookieUrl(url string) bool {
	cookiePatterns := []string{
		"cookielaw.org", "onetrust.com", "quantcast.com", "cookiebot.com",
		"trustarc.com", "cookie-consent", "gdpr", "/privacy", "/consent",
	}
	for _, pattern := range cookiePatterns {
		if strings.Contains(url, pattern) {
			return true
		}
	}
	return false
}

func (s *Service) isChatUrl(url string) bool {
	chatPatterns := []string{
		"intercom.io", "zendesk.com", "livechat.com", "drift.com", "helpscout.com",
		"freshchat.com", "tawk.to", "crisp.chat", "messenger.com", "widget",
		"/chat", "/support", "customer-service",
	}
	for _, pattern := range chatPatterns {
		if strings.Contains(url, pattern) {
			return true
		}
	}
	return false
}

func (s *Service) isTrackerUrl(url string) bool {
	trackerPatterns := []string{
		"google-analytics.com", "googletagmanager.com", "hotjar.com", "mixpanel.com",
		"segment.com", "amplitude.com", "fullstory.com", "logrocket.com",
		"mouseflow.com", "smartlook.com", "/analytics", "/tracking", "/metrics",
		"facebook.com/tr", "linkedin.com/px", "twitter.com/i/adsct",
	}
	for _, pattern := range trackerPatterns {
		if strings.Contains(url, pattern) {
			return true
		}
	}
	return false
}
