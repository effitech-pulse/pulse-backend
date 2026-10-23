package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/gin-gonic/gin"
)

func TestRBACReviewCollectionIntentTooLarge(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	writeCollectionError(c, domain.NewError(domain.Invalid, "collection_intent_too_large"))
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"message":"collection_intent_too_large"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
