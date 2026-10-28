package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"
)

func BenchmarkConfigRender50K(b *testing.B) {
	users := make([]nodeConfigUserSpec, 50000)
	for i := range users {
		users[i] = nodeConfigUserSpec{Name: fmt.Sprintf("scale%05d", i), UUID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i)}
	}
	raw, _ := json.Marshal(users)
	snap := nodeConfigSnapshot{usersJSON: map[string]json.RawMessage{"vless": raw}, core: coreConfigDTO{LogLevel: "warn"}}
	for i := 0; i < 15; i++ {
		snap.inbounds = append(snap.inbounds, nodeConfigInboundSpec{Tag: fmt.Sprintf("in%d", i), Protocol: "vless", ListenPort: uint16(10000 + i), Users: users})
	}
	for _, compact := range []bool{false, true} {
		b.Run(fmt.Sprintf("compact_%v", compact), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			var body []byte
			for i := 0; i < b.N; i++ {
				p, canonical, err := snap.renderSchema(nodeProfile{}, compact)
				if err != nil {
					b.Fatal(err)
				}
				body, err = nodeConfigBodyBytes(p, canonical)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(body))/(1<<20), "MiB/payload")
		})
	}
}
