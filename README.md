# obs-alert-rule-templates

`ServiceObservability` manifestlerindeki SLO hedeflerinden, Google SRE Workbook'un ["Alerting on SLOs"](https://sre.google/workbook/alerting-on-slos/) bölümünde tarif edilen **multi-window, multi-burn-rate** yöntemiyle Prometheus alert kuralları üreten template ve CLI reposudur. Çıktı, Prometheus Operator'ün `PrometheusRule` CRD'sidir; Prometheus/Alertmanager'a herhangi bir API çağrısı yapılmaz.

Bu repo `obs-golden-signal-templates` ile aynı mimari kalıbı izler (Go CLI + `go-jsonnet`, embed edilmiş template'ler, `testdata/manifests`): aynı manifest her zaman byte-for-byte aynı `PrometheusRule` YAML'ını üretir.

## Neden tek eşikli alert değil?

En basit yaklaşım "hata oranı %1'i geçerse sayfaya düş" gibi tek bir eşik koymaktır. Bunun iki temel açmazı vardır:

1. **Gürültü**: Kısa süreli, tek dakikalık bir hata patlaması bile eşiği geçebilir; servis kendi kendine düzelse de sayfa gitmiş olur. Eşiği yükseltirseniz bu sefer gerçek, uzun süreli ama düşük şiddetli sızıntıları (slow burn) kaçırırsınız.
2. **Tepki hızı ile gürültü bağışıklığı çelişir**: Alarmı hızlı tetiklemek için pencereyi kısaltırsanız gürültüye karşı savunmasız kalırsınız; gürültüye karşı dayanıklı olmak için pencereyi uzatırsanız gerçek bir kesintide sayfa dakikalar sonra gelir.

Tek eşikli bir kural bu iki özelliği (gürültüye dayanıklılık + hem hızlı hem yavaş bütçe tükenişini yakalama) aynı anda sağlayamaz.

## Error budget ve burn rate matematiği

SLO hedefi `T` (örn. `0.999`) olan bir servis için **error budget**:

```
budget = 1 - T
```

`T = 0.999` ise 30 günlük pencerede izin verilen hata oranı `budget = 0.001` (%0.1)'dir.

**Burn rate**, gözlenen hata oranının bu bütçeye göre kaç katı hızda tüketildiğini ölçer:

```
burn_rate = observed_error_ratio(window) / budget
```

`burn_rate = 1`, bütçenin tam SLO penceresinin (30 gün) sonunda sıfırlanacak hızda tüketildiği anlamına gelir. `burn_rate = 14.4`, bütçenin `30 gün / 14.4 ≈ 50 saat`de tükeneceği anlamına gelir -- yani hızlı, sayfa gerektiren bir kesinti.

### Neden iki pencere birden (multi-window)?

Her burn-rate katmanı **uzun** bir pencere (örn. `1h`) ve **kısa** bir pencere (örn. `5m`) ile aynı anda kontrol edilir; alarm sadece **ikisi de** eşiği aşarsa tetiklenir (mantıksal VE):

```promql
(
  sum(rate(<error_metric>{service="<svc>",environment="<env>"}[<longWindow>]))
  /
  clamp_min(sum(rate(<traffic_metric>{service="<svc>",environment="<env>"}[<longWindow>])), 1e-9)
) > (<burnRate> * <budget>)
and
(
  sum(rate(<error_metric>{service="<svc>",environment="<env>"}[<shortWindow>]))
  /
  clamp_min(sum(rate(<traffic_metric>{service="<svc>",environment="<env>"}[<shortWindow>])), 1e-9)
) > (<burnRate> * <budget>)
```

- **Uzun pencere** burn'ün gerçek/sürdürülebilir olduğunu, tek dakikalık bir gürültü olmadığını doğrular.
- **Kısa pencere** olay düzeldiği anda alarmın uzun pencerenin süresi kadar beklemeden, neredeyse anında temizlenmesini sağlar.

`clamp_min(..., 1e-9)` paydası, pencerede hiç trafik olmadığında `0/0` NaN'ını engeller.

Bu repo iki sayfa (`page`) katmanı üretir (Workbook'un tam dört katmanlı tablosundan sayfa gerektiren ilk ikisi):

| Katman | Uzun pencere | Kısa pencere | Burn rate | `for` | ~Bütçe tükeniş süresi |
|---|---|---|---|---|---|
| Fast burn | 1h | 5m | 14.4x | 2m | ~2 gün |
| Slow burn | 6h | 30m | 6x | 15m | ~5 gün |

İki katman birbirini **tamamlar**, birbirinin yerine geçmez: ağır bir kesinti sırasında her iki katman da makul şekilde birlikte tetiklenir (bkz. `testdata/promtool/fast-burn.test.yaml`); düşük şiddetli ama sürekli bir sızıntı sadece slow-burn katmanını tetikler, fast-burn'ü tetiklemez (bkz. `testdata/promtool/slow-burn.test.yaml`) -- bu da gürültü azaltmanın somut kanıtıdır.

### Latency SLO ihlali

Burn-rate mantığından farklı olarak latency, tek pencerelik bir eşik aşımıdır (kümülatif bütçe kavramı yoktur), ama gürültü azaltma amacıyla üç farklı pencerede (`5m`, `1h`, `6h`) ayrı ayrı değerlendirilir ve önem derecesi pencere büyüdükçe artar (`warning` → `warning` → `page`): kısa pencerede bir p99 sıçraması muhtemelen geçicidir ve ticket'a düşer; 6 saat boyunca sürmüş bir ihlal artık sistemik bir regresyon olarak kabul edilip sayfaya düşer.

```promql
histogram_quantile(
  <percentile/100>,
  sum(rate(<latency_metric>_bucket{service="<svc>",environment="<env>"}[<window>])) by (le)
) > <latency_target_ms/1000>
```

### Saturation erken uyarısı

CPU/mem/queue gibi doygunluk metrikleri henüz bir SLO ihlaline dönüşmemiş öncü göstergelerdir; bu yüzden her zaman `severity: warning` (ticket'a düşer, sayfaya değil):

```promql
avg_over_time(<saturation_metric>{service="<svc>",environment="<env>"}[10m]) > 0.8
```

**Varsayım**: `goldenSignals.saturationMetrics` altındaki her metrik, `0..1` aralığında normalize edilmiş bir utilization oranı olmalıdır (örn. `cpu_utilization_ratio`, `queue_depth_utilization_ratio`). Ham byte/count gauge'ları (`process_resident_memory_bytes` gibi) kapasiteye göre normalize eden bir recording rule olmadan bu template'le anlamlı şekilde eşiklenemez.

## Mimari

```text
.
├── cmd/gen-alerts/                 # CLI kabuğu
├── internal/manifest/              # ServiceObservability manifest parse + validate (v1 & v2)
├── internal/render/                # Jsonnet render motoru, embed importer, YAML encode
├── templates/
│   ├── lib/burnrate.libsonnet      # Burn-rate/PromQL builder fonksiyonları (yorumlu)
│   └── alert-rules.libsonnet       # Ana parametrik template -> PrometheusRule
├── testdata/
│   ├── manifests/                  # Örnek http ve worker manifestleri
│   └── promtool/                   # `promtool test rules` unit testleri
└── examples/
    └── alertmanager-inhibit-rules.yaml
```

Template girdisi (`internal/render.input`), manifest'ten türetilen sabit bir sözleşmedir:

```json
{
  "name": "checkout-api",
  "serviceType": "http",
  "environment": "prod",
  "team": "team-checkout",
  "availability_target": 0.999,
  "latency_target_ms": 300,
  "latency_percentile": 99,
  "errorMetric": "http_server_request_errors_total",
  "trafficMetric": "http_server_requests_total",
  "latencyMetric": "http_server_request_duration_seconds",
  "saturationMetrics": ["cpu_utilization_ratio", "memory_utilization_ratio"],
  "extraRules": []
}
```

`worker`/`batch` servis tipleri için `goldenSignals.processingDurationMetric`, template'e aynı `latencyMetric` alanı üzerinden akar (`internal/manifest.Manifest.LatencyOrProcessingMetric`).

## Standart label'lar ve runbook konvansiyonu

Üretilen her kural şu label'ları taşır: `team`, `service`, `environment`, `severity` (`page`/`warning`), `slo_target`, `alert_type` (`burn_rate`/`latency_slo`/`saturation`/`extra`). Bu label'lar `obs-notification-router`'ın Alertmanager routing kararlarını (hangi takıma, hangi kanala, PagerDuty mi Slack mi) vermesi için tasarlanmış entegrasyon noktasıdır -- routing mantığının kendisi bu repoda değil, `obs-notification-router`'da yaşar.

Her kuralın `annotations.runbook_url`'ü zorunludur ve sabit konvansiyonu izler:

```
https://runbooks.acme.com/{serviceType}/{alertname}
```

## Kurulum ve kullanım

Gereksinim Go 1.24+.

```bash
make build
./bin/gen-alerts --manifest ./observability.yaml --output ./alerts.yaml
```

Manifestteki `spec.alerting.extraRules` listesi otomatik olarak ayrı bir kural grubuna (`<service>.extra`) render edilir. Manifeste gömülü olmayan, servise özel ek kurallar için ayrı bir dosya da geçilebilir; iki kaynak birleştirilir (manifest önce, dosya sonra):

```bash
./bin/gen-alerts \
  --manifest ./observability.yaml \
  --extra-rules ./service-extra-rules.yaml \
  --output ./alerts.yaml
```

`service-extra-rules.yaml` formatı:

```yaml
extraRules:
  - name: CheckoutQueueBacklogHigh
    expr: checkout_pending_orders_count > 500
    for: 15m
    severity: warning
    summary: Checkout pending-order backlog is elevated
```

Önerilen pipeline sırası:

```bash
obsctl validate observability.yaml
gen-alerts --manifest observability.yaml --output alerts.yaml
kubectl apply -f alerts.yaml
```

## Alert yorgunluğu: `for:` süreleri ve inhibit kuralları

`for:` süreleri her katmanın kendi ihtiyacına göre seçilmiştir (bkz. tablo yukarıda); amaç, alarmı tetiklemeden önce "gerçekten sürdürülebilir mi" sorusuna kısa ama anlamlı bir doğrulama penceresi tanımaktır.

Bunun tek başına yetmediği durum şudur: bir servis tamamen down olduğunda burn-rate, latency-SLO ve saturation alertleri **aynı anda** tetiklenebilir ve on-call kişisi aynı kök nedenin üç ayrı bildirimini alır. `examples/alertmanager-inhibit-rules.yaml`, bunu Alertmanager'ın `inhibit_rules` mekanizmasıyla çözer:

- Fast-burn (`severity=page`) tetiklendiğinde, aynı `service`+`environment` için latency-SLO ve saturation alertleri bastırılır.
- Herhangi bir burn-rate page'i, aynı servis için saturation warning'lerini bastırır.
- Genel bir "sayfa varsa aynı servisin ticket'ları bastırılsın" kuralı, adı geçmeyen gelecekteki alert tiplerini de kapsar.

Latency-SLO alertleri slow-burn tarafından **kasıtlı olarak** bastırılmaz: bir slow burn sadece hata oranını etkileyip latency'i etkilemeyebilir (örn. sessizce başarısız olan bir arka plan işi), bu yüzden latency kendi başına aksiyoneldir.

## Testler

```bash
make test              # go test ./...
make verify            # gofmt + go vet + go test + build
make promtool-test     # promtool test rules testdata/promtool/*.test.yaml (promtool PATH'te olmalı)
```

`testdata/promtool/rules/burn-rate-rules.yaml`, `gen-alerts`'in `testdata/manifests/http.yaml` için ürettiği gerçek `burn-rate` grubunun bir kopyasıdır (elle yazılmış yaklaşık bir kural değil) -- bu yüzden promtool testleri template'in ürettiği gerçek PromQL'i doğrular:

- `fast-burn.test.yaml`: ~%10 sabit hata oranının hem fast hem slow katmanı (makul şekilde) birlikte tetiklediğini, ve pencere henüz dolmamışken (`t=1m`) hiçbir alarmın tetiklenmediğini kanıtlar.
- `slow-burn.test.yaml`: %0.8 sabit hata oranının sadece slow-burn katmanını tetiklediğini, fast-burn eşiğinin (%1.44) altında kaldığı için fast-burn'ün sessiz kaldığını kanıtlar -- gürültü azaltmanın somut kanıtı.

`internal/render/render_test.go`, Go tarafında hem `http` hem `worker` manifestlerinin doğru render edildiğini ve `--extra-rules` birleştirmesinin çalıştığını doğrular.

## Yeni bir katman veya servis tipi eklemek

1. `templates/lib/burnrate.libsonnet`'e yeni bir PromQL builder ekleyin (matematiği bir yorumla açıklayın).
2. `templates/alert-rules.libsonnet` içindeki ilgili `*Tiers` listesine yeni katmanı ekleyin veya yeni bir `*Rule` fonksiyonu yazın.
3. `internal/render.input` sözleşmesi değişiyorsa `internal/render/render.go` ve `internal/manifest/manifest.go`'yu güncelleyin.
4. `testdata/manifests/`e gerçekçi bir manifest ekleyin, `internal/render/render_test.go`'ya bir örnek ekleyin.
5. Yeni alert için `testdata/promtool/`e sentetik zaman serili bir unit test ekleyin; `make promtool-test` ile doğrulayın.
6. `make verify` çalıştırın.
