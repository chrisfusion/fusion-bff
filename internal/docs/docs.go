// Package docs serves the hand-written OpenAPI spec and a Swagger UI for it.
// The spec is maintained by hand in openapi.yaml; nothing here generates or
// alters the spec from code, so existing handlers/routes are unaffected.
package docs

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

//go:embed openapi.yaml
var spec []byte

// SpecHandler serves the raw OpenAPI spec.
func SpecHandler(c *gin.Context) {
	c.Data(http.StatusOK, "application/yaml", spec)
}

// UIHandler serves Swagger UI pointed at the spec above.
func UIHandler() gin.HandlerFunc {
	return ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL("/bff/openapi.yaml"))
}
