package dispatch

import (
	"reflect"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// unionMembers maps the S3 union interfaces the corpus exercises to their
// member key -> concrete member struct (each member struct has a Value
// field). Extend as corpus coverage grows; the offline smoke test flags any
// union it cannot decode.
var unionMembers = map[reflect.Type]map[string]reflect.Type{
	reflect.TypeFor[s3types.MetricsFilter](): {
		"Prefix":         reflect.TypeFor[s3types.MetricsFilterMemberPrefix](),
		"Tag":            reflect.TypeFor[s3types.MetricsFilterMemberTag](),
		"And":            reflect.TypeFor[s3types.MetricsFilterMemberAnd](),
		"AccessPointArn": reflect.TypeFor[s3types.MetricsFilterMemberAccessPointArn](),
	},
	reflect.TypeFor[s3types.AnalyticsFilter](): {
		"Prefix": reflect.TypeFor[s3types.AnalyticsFilterMemberPrefix](),
		"Tag":    reflect.TypeFor[s3types.AnalyticsFilterMemberTag](),
		"And":    reflect.TypeFor[s3types.AnalyticsFilterMemberAnd](),
	},
}
