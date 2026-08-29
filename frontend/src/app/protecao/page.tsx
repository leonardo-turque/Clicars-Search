'use client'

import { useCallback, useEffect, useState } from 'react'
import { API_URL, digitsOnly, formatPhone } from '@/lib/format'

type Stage = 'WARMING' | 'MATURE' | 'PAUSED' | 'COOLING'

interface Snapshot {
  session_id: string
  phone_number: string
  stage: Stage
  warmup_day: number
  daily_cap: number
  target_daily: number
  health_score: number
  sent_today: number
  remaining_today: number
  hourly_sent: number
  hourly_cap: number
  burst_count: number
  warmup_sent_today: number
  phase?: string
  phase_label?: string
  campaign_budget_today?: number
  warmup_budget_today?: number
  campaign_sent_today?: number
  status_reason: string
  connected: boolean
  mature_seed: boolean
  circuit_open_until?: string
  last_error?: string
  last_sent_at?: string
}

interface Contact {
  id: string
  phone: string
  label?: string
  active: boolean
  last_sent_at?: string
}

function Spinner({ className = 'h-4 w-4' }: { className?: string }) {
  return (
    <svg className={`animate-spin ${className}`} xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
    </svg>
  )
}

function stageMeta(stage: Stage) {
  return {
    WARMING: { label: 'Aquecendo', bg: 'bg-amber-50', text: 'text-amber-800', bar: 'bg-amber-500' },
    MATURE: { label: 'Maduro', bg: 'bg-green-50', text: 'text-green-800', bar: 'bg-green-500' },
    PAUSED: { label: 'Pausado', bg: 'bg-gray-100', text: 'text-gray-700', bar: 'bg-gray-400' },
    COOLING: { label: 'Resfriando', bg: 'bg-red-50', text: 'text-red-700', bar: 'bg-red-500' },
  }[stage]
}

function HealthBar({ score }: { score: number }) {
  const color = score >= 80 ? 'bg-green-500' : score >= 50 ? 'bg-amber-500' : 'bg-red-500'
  return (
    <div className="h-2 w-full overflow-hidden rounded-full bg-gray-100">
      <div className={`h-full ${color} transition-all`} style={{ width: `${Math.max(0, Math.min(100, score))}%` }} />
    </div>
  )
}

function NumberCard({
  snap,
  busy,
  onPause,
  onResume,
}: {
  snap: Snapshot
  busy: boolean
  onPause: () => void
  onResume: () => void
}) {
  const meta = stageMeta(snap.stage)
  const usedPct = snap.daily_cap > 0 ? Math.min(100, Math.round((snap.sent_today / snap.daily_cap) * 100)) : 0
  const primary = digitsOnly(snap.phone_number) === '554184376916'

  return (
    <article className={`surface p-5 flex flex-col gap-4 ${primary ? 'ring-2 ring-amber-200' : ''}`}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-base font-semibold text-gray-900 truncate">{formatPhone(snap.phone_number)}</p>
          <p className="mt-1 text-xs text-gray-500">{snap.status_reason}</p>
        </div>
        <span className={`inline-flex items-center rounded-full px-2.5 py-1 text-xs font-semibold ${meta.bg} ${meta.text}`}>
          {meta.label}
        </span>
      </div>

      {primary && (
        <p className="rounded-xl bg-amber-50 px-3 py-2 text-xs font-medium text-amber-900">
          Número principal — passa pelas 4 fases de aquecimento. Campanhas só começam na fase 2 (dia 4).
        </p>
      )}
      {snap.phase_label && (
        <p className="text-xs font-semibold text-gray-700">{snap.phase_label}</p>
      )}

      <div>
        <div className="mb-1.5 flex items-center justify-between text-xs text-gray-500">
          <span>Hoje</span>
          <span className="font-semibold text-gray-800">
            {snap.sent_today}/{snap.daily_cap} · restam {snap.remaining_today}
          </span>
        </div>
        <div className="h-2.5 w-full overflow-hidden rounded-full bg-gray-100">
          <div className={`h-full ${meta.bar} transition-all`} style={{ width: `${usedPct}%` }} />
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3 text-xs">
        <div className="rounded-xl bg-gray-50 px-3 py-2">
          <p className="font-bold uppercase tracking-wide text-gray-400">Saúde</p>
          <p className="mt-1 text-sm font-semibold text-gray-900">{snap.health_score}/100</p>
          <div className="mt-2"><HealthBar score={snap.health_score} /></div>
        </div>
        <div className="rounded-xl bg-gray-50 px-3 py-2">
          <p className="font-bold uppercase tracking-wide text-gray-400">Nesta hora</p>
          <p className="mt-1 text-sm font-semibold text-gray-900">{snap.hourly_sent}/{snap.hourly_cap}</p>
          <p className="mt-1 text-[11px] text-gray-500">
            {snap.stage === 'WARMING' ? `Dia ${snap.warmup_day} de 21` : 'Ritmo maduro'}
          </p>
        </div>
      </div>

      {snap.stage === 'WARMING' && (
        <div>
          <div className="mb-1 flex justify-between text-[11px] text-gray-500">
            <span>Rampa de aquecimento</span>
            <span>{Math.min(21, snap.warmup_day)}/21</span>
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-gray-100">
            <div className="h-full bg-amber-500" style={{ width: `${Math.min(100, (snap.warmup_day / 21) * 100)}%` }} />
          </div>
        </div>
      )}

      {snap.last_error && (
        <p className="truncate text-xs text-red-600" title={snap.last_error}>Último erro: {snap.last_error}</p>
      )}

      <div className="mt-auto flex gap-2">
        {snap.stage === 'PAUSED' ? (
          <button
            onClick={onResume}
            disabled={busy}
            className="w-full rounded-xl bg-green-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-green-700 disabled:opacity-50"
          >
            Retomar
          </button>
        ) : (
          <button
            onClick={onPause}
            disabled={busy}
            className="w-full rounded-xl border border-gray-200 px-4 py-2.5 text-sm font-semibold text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          >
            Pausar envios
          </button>
        )}
      </div>
    </article>
  )
}

export default function ProtectPage() {
  const [numbers, setNumbers] = useState<Snapshot[]>([])
  const [contacts, setContacts] = useState<Contact[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [phone, setPhone] = useState('')
  const [label, setLabel] = useState('')
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const [nRes, cRes] = await Promise.all([
        fetch(`${API_URL}/api/v1/protect/numbers`),
        fetch(`${API_URL}/api/v1/protect/contacts`),
      ])
      if (!nRes.ok) throw new Error(`Erro ${nRes.status}`)
      const nData: Snapshot[] = await nRes.json()
      setNumbers(nData ?? [])
      if (cRes.ok) {
        const cData: Contact[] = await cRes.json()
        setContacts(cData ?? [])
      }
      setError(null)
    } catch (err) {
      if (err instanceof TypeError) {
        setError('Não foi possível conectar à API em ' + API_URL)
      } else {
        setError(err instanceof Error ? err.message : 'Erro inesperado.')
      }
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
    const t = setInterval(load, 8000)
    return () => clearInterval(t)
  }, [load])

  async function flip(id: string, pause: boolean) {
    setBusyId(id)
    try {
      const res = await fetch(`${API_URL}/api/v1/protect/numbers/${id}/${pause ? 'pause' : 'resume'}`, { method: 'POST' })
      if (!res.ok && res.status !== 204) {
        const body = await res.json().catch(() => null)
        throw new Error(body?.error || `Erro ${res.status}`)
      }
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Falha ao atualizar o número.')
    } finally {
      setBusyId(null)
    }
  }

  async function addContact(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    setError(null)
    try {
      const res = await fetch(`${API_URL}/api/v1/protect/contacts`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ phone, label }),
      })
      const body = await res.json().catch(() => null)
      if (!res.ok) throw new Error(body?.error || `Erro ${res.status}`)
      setPhone('')
      setLabel('')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Falha ao salvar o contato.')
    } finally {
      setSaving(false)
    }
  }

  async function removeContact(id: string) {
    try {
      const res = await fetch(`${API_URL}/api/v1/protect/contacts/${id}`, { method: 'DELETE' })
      if (!res.ok && res.status !== 204) throw new Error(`Erro ${res.status}`)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Falha ao remover o contato.')
    }
  }

  const mature = numbers.filter((n) => n.stage === 'MATURE').length
  const remaining = numbers.reduce((sum, n) => sum + n.remaining_today, 0)

  return (
    <main className="min-h-screen">
      <div className="page-shell">
        <div className="page-heading">
          <div>
            <p className="eyebrow">Motor anti-banimento</p>
            <h1 className="page-title">Proteção e aquecimento</h1>
            <p className="page-lead">
              O {formatPhone('554184376916')} entra no dia 1 da rampa de 21 dias. Nos três primeiros dias só conversa com contatos de confiança — campanhas ficam na fila.
            </p>
          </div>
        </div>

        <div className="metric-grid">
          <div className="metric-card">
            <small>Números no radar</small>
            <strong>{numbers.length}</strong>
          </div>
          <div className="metric-card">
            <small>Maduros</small>
            <strong>{mature}</strong>
          </div>
          <div className="metric-card">
            <small>Cota restante hoje</small>
            <strong>{remaining}</strong>
          </div>
        </div>

        <div className="notice-safe mb-6 rounded-2xl px-4 py-4 text-sm leading-relaxed sm:px-5">
          <strong>Como o aquecimento funciona:</strong> fase 1 (dias 1–3) só pings para contatos seus;
          fase 2 (4–7) libera uma fatia pequena de campanha; fase 3 (8–14) mistura; fase 4 (15–21) sobe até 200.
          Sem contatos de confiança abaixo, o chip não “conversa” e a rampa fica só no limite de volume — cadastre pelo menos 3.
        </div>

        {error && (
          <div className="mb-6 rounded-xl border border-red-200 bg-red-50 px-5 py-4 text-sm text-red-700">{error}</div>
        )}

        {loading ? (
          <div className="flex justify-center py-16 text-gray-400"><Spinner className="h-8 w-8" /></div>
        ) : numbers.length === 0 ? (
          <div className="surface px-6 py-16 text-center text-sm text-gray-500">
            Nenhum número no motor ainda. Conecte o {formatPhone('554184376916')} na aba Números — ele começa no dia 1, fase Identidade.
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            {numbers.map((n) => (
              <NumberCard
                key={n.session_id}
                snap={n}
                busy={busyId === n.session_id}
                onPause={() => flip(n.session_id, true)}
                onResume={() => flip(n.session_id, false)}
              />
            ))}
          </div>
        )}

        <section className="surface mt-6 p-4 sm:p-6">
          <p className="eyebrow">Aquecimento automático</p>
          <h2 className="text-lg font-semibold text-gray-900">Contatos de confiança</h2>
          <p className="mt-1 mb-5 text-sm text-gray-500">
            O worker envia mensagens curtas e variadas para estes números durante a rampa. Números maduros recebem no máximo 2 pings por dia.
          </p>

          <form onSubmit={addContact} className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
            <div>
              <label htmlFor="wu-phone" className="mb-1.5 block text-sm font-medium text-gray-700">Telefone</label>
              <input
                id="wu-phone"
                value={phone}
                onChange={(e) => setPhone(e.target.value)}
                placeholder="55 41 99999-0000"
                required
                className="w-full rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-900 focus:bg-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <div>
              <label htmlFor="wu-label" className="mb-1.5 block text-sm font-medium text-gray-700">Apelido (opcional)</label>
              <input
                id="wu-label"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                placeholder="Chip pessoal, escritório…"
                className="w-full rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-900 focus:bg-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <button
              type="submit"
              disabled={saving || !phone.trim()}
              className="inline-flex items-center justify-center rounded-xl bg-blue-600 px-5 py-3 text-sm font-semibold text-white hover:bg-blue-700 disabled:bg-blue-300"
            >
              {saving ? <Spinner /> : 'Adicionar'}
            </button>
          </form>

          {contacts.length === 0 ? (
            <p className="mt-5 text-sm text-gray-400">Nenhum contato ainda. Sem eles, o aquecimento só controla o volume das campanhas.</p>
          ) : (
            <ul className="mt-5 divide-y divide-gray-100">
              {contacts.map((c) => (
                <li key={c.id} className="flex items-center justify-between gap-3 py-3">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium text-gray-900">{formatPhone(c.phone)}</p>
                    <p className="text-xs text-gray-500">{c.label || 'Sem apelido'}</p>
                  </div>
                  <button
                    onClick={() => removeContact(c.id)}
                    className="rounded-lg px-3 py-2 text-xs font-semibold text-red-600 hover:bg-red-50"
                  >
                    Remover
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </main>
  )
}
