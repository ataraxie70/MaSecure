# MaSecure — Infrastructure de Règlement Social Automatisé

> Tontines automatisées à confiance décentralisée.  
> La confiance n'est plus portée par une personne — elle est portée par des invariants, des preuves et des frontières de privilèges.

---

## Architecture en un coup d'œil

```
WhatsApp / SMS
      │ UserIntent seulement (jamais PayoutCommand)
      ▼
┌─────────────────┐    ┌──────────────────────┐    ┌─────────────────┐
│ Gateway (Go)    │    │  Service Social (Go)  │    │  Kernel (Rust)  │
│ HMAC vérifié    │───▶│  Phase 3 Gouvernance  │───▶│  Ledger         │
│ Parse intentions│    │  Phase 3 Audit export │    │  PayoutCommand  │
└─────────────────┘    │  Phase 4 Dashboard    │    │  ResilienceEval │
                       │  Phase 5 Monitoring   │    └────────┬────────┘
                       └──────────────────────┘             │ Outbox (atomique)
                                                            ▼
                                                   ┌─────────────────┐
                                                   │ Outbox Worker   │
                                                   │ Retry + backoff │
                                                   │ Notifications   │◀── Phase 5
                                                   └────────┬────────┘
                                                            │
                                                            ▼
                                                   Mobile Money API
                                                   (Orange / Moov / Wave)
                                                   Adaptateurs réels Phase 2
```

## État des Phases

### Phase 1 — MVP Socle ✅ Complète

Kernel financier Rust, ledger append-only, scheduler de payout, outbox transactionnelle,
endpoint interne de traitement des contributions et de confirmation de payout,
callback public Go qui valide le HMAC puis transfère au kernel, garde-fous anti-contribution
hors cycle `committed`, vérificateur de ledger opérationnel.

### Phase 2 — Paiement Mobile Money ✅ Complète

Mode `MOBILE_MONEY_MODE=live`, contrat HTTP sortant signé, callbacks provider-specific
(Orange, Moov, Wave), simulateur local validé. Adaptateurs réels :
`internal/mobilemoney/orangemoney`, `moovmoney`, `wavemoney`.

### Phase 3 — Gouvernance ✅ Complète

Propositions de changement de configuration, diff automatique, vote par quorum,
commit atomique vers `active_config_id`, expiration automatique, export d'audit
(cycles + ledger + contributions + hash de rapport).

### Phase 4 — Résilience ✅ Complète

Fonds de roulement (avance totale/partielle), versement pro-rata, politique hybride,
créances membres avec remboursement automatique, ResilienceScheduler, dashboard temps réel.

### Phase 5 — Industrialisation ✅ Complète

Notifications WhatsApp Business API + SMS fallback réelles, détection d'anomalies
(5 contrôles automatiques), archives légales BCEAO signées, AML/KYC simplifié,
monitoring opérationnel `ops_health_dashboard`.

## Démarrage rapide

```bash
make dev              # Lance PG + NATS + migrations
make kernel-run       # Kernel Rust (port 8001)
make api-run          # API Gateway Go (port 8000)
make social-run       # Social Service (port 8002)
make outbox-run       # Outbox Worker
```

---

> ⚠️ **Avertissement réglementaire** : Ce système gère des flux d'argent réels.
> Un avis juridique spécialisé est requis avant tout déploiement en production.
