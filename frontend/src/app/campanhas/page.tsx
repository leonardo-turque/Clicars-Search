'use client'

import { useState, useEffect, useCallback, useRef } from 'react'

type CampaignStatus = 'PENDING' | 'RUNNING' | 'COMPLETED'

interface CampaignSummary {
  id: string
  search_id: string
  whatsapp_session_id: string
  message_body: string
  status: CampaignStatus
  total: number
  sent: number
  failed: number
  progress: string
  created_at: string
  search_niche: string
  search_location: string
  phone_number: string
}

interface SearchSummary {
  id: string
  niche: string
  location: string
  quantity: number
  created_at: string
}

interface WASession {
  id: string
  phone_number: string
  status: 'CONNECTING' | 'CONNECTED' | 'DISCONNECTED'
}

interface SearchDetail {
  companies: { phone: string }[]
}

const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'
const POLL_MS = 2500
const MAX_MESSAGE = 1000
const AVG_DELAY_S = 60

// ---- Utility components ----

function Spinner({ className = 'h-4 w-4' }: { className?: string }) {
  return (
    <svg className={`animate-spin ${className}`} xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
    </svg>
  )
}

function StatusBadge({ status }: { status: CampaignStatus }) {
  const map = {
    PENDING:   { bg: 'bg-amber-50',  text: 'text-amber-700',  dot: 'bg-amber-400',  label: 'Pendente',   pulse: true  },
    RUNNING:   { bg: 'bg-blue-50',   text: 'text-blue-700',   dot: 'bg-blue-500',   label: 'Enviando',   pulse: true  },
    COMPLETED: { bg: 'bg-green-50',  text: 'text-green-700',  dot: 'bg-green-500',  label: 'Finalizado', pulse: false },
  }[status]
  return (
    <span className={`inline-flex items-center gap-1.5 ${map.bg} ${map.text} rounded-full px-2.5 py-1 text-xs font-medium`}>
      <span className={`h-2 w-2 rounded-full ${map.dot} ${map.pulse ? 'animate-pulse' : ''}`} />
      {map.label}
    </span>
  )
}

function Toast({ message, type, onDismiss }: { message: string; type: 'success' | 'error'; onDismiss: () => void }) {
  useEffect(() => {
    const t = setTimeout(onDismiss, 4000)
    return () => clearTimeout(t)
  }, [onDismiss])
  return (
    <div className={`fixed bottom-6 right-6 z-50 flex items-center gap-3 rounded-xl px-4 py-3 shadow-lg text-sm font-medium
      ${type === 'success' ? 'bg-green-600 text-white' : 'bg-red-600 text-white'}`}>
      {type === 'success'
        ? <svg className="h-4 w-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" /></svg>
        : <svg className="h-4 w-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" /></svg>
      }
      {message}
      <button onClick={onDismiss} className="ml-2 opacity-75 hover:opacity-100">
        <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" /></svg>
      </button>
    </div>
  )
}

function formatPhone(raw: string): string {
  if (!raw) return '—'
  const d = raw.replace(/\D/g, '')
  if (d.length >= 12 && d.startsWith('55')) {
    const ddd = d.slice(2, 4)
    const rest = d.slice(4)
    return `+55 (${ddd}) ${rest.slice(0, rest.length - 4)}-${rest.slice(-4)}`
  }
  return `+${d}`
}

function formatEstimatedTime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  const m = Math.round(seconds / 60)
  if (m < 60) return `~${m} min`
  const h = Math.floor(m / 60)
  const rem = m % 60
  return rem > 0 ? `~${h}h ${rem}min` : `~${h}h`
}

// ---- Progress bar ----

function ProgressBar({ sent, failed, total }: { sent: number; failed: number; total: number }) {
  const attempted = sent + failed
  const pct = total > 0 ? Math.round((attempted / total) * 100) : 0
  const sentPct = total > 0 ? Math.round((sent / total) * 100) : 0
  const failedPct = total > 0 ? Math.round((failed / total) * 100) : 0
  return (
    <div className="w-full min-w-[120px]">
      <div className="flex items-center justify-between mb-1 text-xs text-gray-500">
        <span>{attempted}/{total}</span>
        <span>{pct}%</span>
      </div>
      <div className="h-2 w-full rounded-full bg-gray-100 overflow-hidden flex">
        <div className="h-full bg-green-500 transition-all duration-500" style={{ width: `${sentPct}%` }} />
        <div className="h-full bg-red-400 transition-all duration-500" style={{ width: `${failedPct}%` }} />
      </div>
    </div>
  )
}

// ---- Details Modal ----

function DetailsModal({ campaign, onClose }: { campaign: CampaignSummary; onClose: () => void }) {
  const attempted = campaign.sent + campaign.failed
  const pct = campaign.total > 0 ? Math.round((attempted / campaign.total) * 100) : 0
  return (
    <div className="fixed inset-0 z-30 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true" onClick={onClose}>
      <div className="bg-white rounded-2xl shadow-xl w-full max-w-lg p-6 relative" onClick={(e) => e.stopPropagation()}>
        <button onClick={onClose} aria-label="Fechar" className="absolute top-4 right-4 text-gray-400 hover:text-gray-600">
          <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>

        <h2 className="text-lg font-semibold text-gray-900 mb-4">Detalhes da Campanha</h2>

        <dl className="space-y-3 text-sm">
          <div className="flex gap-2">
            <dt className="w-36 font-medium text-gray-500 flex-shrink-0">ID</dt>
            <dd className="font-mono text-xs text-blue-700 bg-blue-50 px-2 py-0.5 rounded break-all">{campaign.id}</dd>
          </div>
          <div className="flex gap-2">
            <dt className="w-36 font-medium text-gray-500 flex-shrink-0">Busca de Origem</dt>
            <dd className="text-gray-800">{campaign.search_niche} · {campaign.search_location}</dd>
          </div>
          <div className="flex gap-2">
            <dt className="w-36 font-medium text-gray-500 flex-shrink-0">Número Remetente</dt>
            <dd className="text-gray-800">{campaign.phone_number ? formatPhone(campaign.phone_number) : '—'}</dd>
          </div>
          <div className="flex gap-2">
            <dt className="w-36 font-medium text-gray-500 flex-shrink-0">Status</dt>
            <dd><StatusBadge status={campaign.status} /></dd>
          </div>
          <div className="flex gap-2">
            <dt className="w-36 font-medium text-gray-500 flex-shrink-0">Data</dt>
            <dd className="text-gray-800">{new Date(campaign.created_at).toLocaleString('pt-BR')}</dd>
          </div>
        </dl>

        <div className="mt-5">
          <p className="text-xs font-medium text-gray-500 uppercase tracking-wider mb-2">Progresso</p>
          <div className="flex items-center gap-4 mb-2">
            <div className="flex-1">
              <div className="h-3 w-full rounded-full bg-gray-100 overflow-hidden flex">
                <div className="h-full bg-green-500 transition-all duration-500" style={{ width: `${campaign.total > 0 ? Math.round((campaign.sent / campaign.total) * 100) : 0}%` }} />
                <div className="h-full bg-red-400 transition-all duration-500" style={{ width: `${campaign.total > 0 ? Math.round((campaign.failed / campaign.total) * 100) : 0}%` }} />
              </div>
            </div>
            <span className="text-sm font-medium text-gray-700">{pct}%</span>
          </div>
          <div className="flex gap-4 text-sm">
            <span className="text-green-600 font-medium">{campaign.sent} enviados</span>
            {campaign.failed > 0 && <span className="text-red-500 font-medium">{campaign.failed} falharam</span>}
            <span className="text-gray-400">{campaign.total} total</span>
          </div>
        </div>

        <div className="mt-5">
          <p className="text-xs font-medium text-gray-500 uppercase tracking-wider mb-2">Mensagem</p>
          <div className="bg-gray-50 rounded-lg border border-gray-200 px-4 py-3 text-sm text-gray-700 whitespace-pre-wrap max-h-40 overflow-y-auto">
            {campaign.message_body}
          </div>
        </div>

        <div className="mt-5 flex justify-end">
          <button onClick={onClose} className="px-5 py-2 rounded-lg bg-gray-100 hover:bg-gray-200 text-gray-700 text-sm font-medium transition-colors">
            Fechar
          </button>
        </div>
      </div>
    </div>
  )
}

// ---- Campaign Creation Wizard ----

type WizardStep = 1 | 2 | 3

interface WizardState {
  selectedSearch: SearchSummary | null
  selectedSessionId: string
  message: string
  leadCount: number | null
  loadingLeads: boolean
}

function NewCampaignWizard({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated: (c: CampaignSummary) => void
}) {
  const [step, setStep] = useState<WizardStep>(1)
  const [searches, setSearches] = useState<SearchSummary[]>([])
  const [sessions, setSessions] = useState<WASession[]>([])
  const [loadingSearches, setLoadingSearches] = useState(true)
  const [loadingSessions, setLoadingSessions] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [state, setState] = useState<WizardState>({
    selectedSearch: null,
    selectedSessionId: '',
    message: '',
    leadCount: null,
    loadingLeads: false,
  })

  useEffect(() => {
    Promise.all([
      fetch(`${API_URL}/api/v1/searches`).then((r) => r.json()).catch(() => []),
      fetch(`${API_URL}/api/v1/whatsapp/sessions`).then((r) => r.json()).catch(() => []),
    ]).then(([searchData, sessionData]) => {
      setSearches((searchData as SearchSummary[]) ?? [])
      setLoadingSearches(false)
      const connected = ((sessionData as WASession[]) ?? []).filter((s) => s.status === 'CONNECTED')
      setSessions(connected)
      if (connected.length > 0) setState((p) => ({ ...p, selectedSessionId: connected[0].id }))
      setLoadingSessions(false)
    })
  }, [])

  const fetchLeadCount = useCallback(async (searchId: string) => {
    setState((p) => ({ ...p, loadingLeads: true, leadCount: null }))
    try {
      const res = await fetch(`${API_URL}/api/v1/searches/${searchId}`)
      if (!res.ok) return
      const data: SearchDetail = await res.json()
      const withPhone = (data.companies ?? []).filter((c) => c.phone && c.phone.trim() !== '').length
      setState((p) => ({ ...p, leadCount: withPhone, loadingLeads: false }))
    } catch {
      setState((p) => ({ ...p, loadingLeads: false }))
    }
  }, [])

  const selectSearch = useCallback((s: SearchSummary) => {
    setState((p) => ({ ...p, selectedSearch: s }))
    fetchLeadCount(s.id)
  }, [fetchLeadCount])

  const handleSubmit = useCallback(async () => {
    if (!state.selectedSearch || !state.selectedSessionId || !state.message.trim()) return
    setSubmitting(true)
    setError(null)
    try {
      const res = await fetch(`${API_URL}/api/v1/campaigns`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          search_id: state.selectedSearch.id,
          whatsapp_session_id: state.selectedSessionId,
          message: state.message,
        }),
      })
      const body = await res.json().catch(() => null)
      if (!res.ok) throw new Error(body?.error || `Erro ${res.status}`)
      onCreated(body as CampaignSummary)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Falha ao iniciar a campanha.')
      setSubmitting(false)
    }
  }, [state, onCreated])

  const estimatedSeconds = state.leadCount != null ? state.leadCount * AVG_DELAY_S : null

  return (
    <div className="fixed inset-0 z-30 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true" onClick={onClose}>
      <div className="bg-white rounded-2xl shadow-xl w-full max-w-lg relative" onClick={(e) => e.stopPropagation()}>
        {/* Header */}
        <div className="px-6 pt-6 pb-4 border-b border-gray-100">
          <button onClick={onClose} aria-label="Fechar" className="absolute top-4 right-4 text-gray-400 hover:text-gray-600">
            <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
          <h2 className="text-lg font-semibold text-gray-900">Nova Campanha</h2>
          {/* Step indicator */}
          <div className="flex items-center gap-2 mt-3">
            {([1, 2, 3] as WizardStep[]).map((s) => (
              <div key={s} className="flex items-center gap-2">
                <div className={`h-6 w-6 rounded-full flex items-center justify-center text-xs font-semibold transition-colors
                  ${step === s ? 'bg-blue-600 text-white' : step > s ? 'bg-green-500 text-white' : 'bg-gray-100 text-gray-400'}`}>
                  {step > s ? (
                    <svg className="h-3.5 w-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2.5} d="M5 13l4 4L19 7" />
                    </svg>
                  ) : s}
                </div>
                <span className={`text-xs ${step === s ? 'text-gray-800 font-medium' : 'text-gray-400'}`}>
                  {s === 1 ? 'Origem' : s === 2 ? 'Mensagem' : 'Revisão'}
                </span>
                {s < 3 && <div className="h-px w-6 bg-gray-200" />}
              </div>
            ))}
          </div>
        </div>

        {/* Step content */}
        <div className="p-6">
          {/* Step 1: Select origin search */}
          {step === 1 && (
            <div>
              <p className="text-sm text-gray-500 mb-4">Selecione a busca cujos leads receberão a mensagem.</p>
              {loadingSearches ? (
                <div className="flex justify-center py-8"><Spinner className="h-6 w-6 text-gray-400" /></div>
              ) : searches.length === 0 ? (
                <p className="text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded-lg px-4 py-3">
                  Nenhuma busca encontrada. Realize uma busca primeiro na aba <strong>Busca</strong>.
                </p>
              ) : (
                <ul className="space-y-2 max-h-64 overflow-y-auto pr-1">
                  {searches.map((s) => (
                    <li key={s.id}>
                      <button
                        onClick={() => selectSearch(s)}
                        className={`w-full text-left px-4 py-3 rounded-xl border transition-colors
                          ${state.selectedSearch?.id === s.id
                            ? 'border-blue-400 bg-blue-50'
                            : 'border-gray-200 hover:border-blue-200 hover:bg-gray-50'
                          }`}
                      >
                        <p className="text-sm font-medium text-gray-900">{s.niche}</p>
                        <p className="text-xs text-gray-500 mt-0.5">{s.location} · {s.quantity} empresa(s)</p>
                        <p className="text-xs text-gray-400 mt-0.5">
                          {new Date(s.created_at).toLocaleDateString('pt-BR', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })}
                        </p>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}

          {/* Step 2: WA session + message */}
          {step === 2 && (
            <div className="flex flex-col gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1.5">Número de WhatsApp</label>
                {loadingSessions ? (
                  <div className="flex items-center gap-2 text-sm text-gray-400 py-2"><Spinner /> Carregando…</div>
                ) : sessions.length === 0 ? (
                  <p className="text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded-lg px-3 py-2">
                    Nenhum número conectado. Conecte um na aba <strong>WhatsApp</strong>.
                  </p>
                ) : (
                  <select
                    value={state.selectedSessionId}
                    onChange={(e) => setState((p) => ({ ...p, selectedSessionId: e.target.value }))}
                    className="w-full px-4 py-2.5 rounded-lg border border-gray-200 bg-gray-50 focus:bg-white focus:outline-none focus:ring-2 focus:ring-blue-500 text-gray-900 text-sm"
                  >
                    {sessions.map((s) => (
                      <option key={s.id} value={s.id}>{formatPhone(s.phone_number)}</option>
                    ))}
                  </select>
                )}
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1.5">Mensagem</label>
                <textarea
                  rows={5}
                  maxLength={MAX_MESSAGE}
                  placeholder="Olá! Temos uma oferta especial para a sua empresa…"
                  value={state.message}
                  onChange={(e) => setState((p) => ({ ...p, message: e.target.value }))}
                  className="w-full px-4 py-2.5 rounded-lg border border-gray-200 bg-gray-50 focus:bg-white focus:outline-none focus:ring-2 focus:ring-blue-500 text-gray-900 placeholder-gray-400 text-sm resize-none"
                />
                <p className="text-xs text-gray-400 mt-1 text-right">{state.message.length}/{MAX_MESSAGE}</p>
              </div>
            </div>
          )}

          {/* Step 3: Review & start */}
          {step === 3 && (
            <div className="flex flex-col gap-4">
              <div className="rounded-xl bg-gray-50 border border-gray-200 divide-y divide-gray-200">
                <div className="px-4 py-3 flex justify-between text-sm">
                  <span className="text-gray-500">Busca de origem</span>
                  <span className="font-medium text-gray-900">{state.selectedSearch?.niche} · {state.selectedSearch?.location}</span>
                </div>
                <div className="px-4 py-3 flex justify-between text-sm">
                  <span className="text-gray-500">Número remetente</span>
                  <span className="font-medium text-gray-900">
                    {formatPhone(sessions.find((s) => s.id === state.selectedSessionId)?.phone_number ?? '')}
                  </span>
                </div>
                <div className="px-4 py-3 flex justify-between text-sm">
                  <span className="text-gray-500">Leads a impactar</span>
                  <span className="font-medium text-gray-900">
                    {state.loadingLeads ? <Spinner className="h-3.5 w-3.5 inline-block" /> : state.leadCount != null ? `~${state.leadCount} com telefone` : '—'}
                  </span>
                </div>
                <div className="px-4 py-3 flex justify-between text-sm">
                  <span className="text-gray-500">Tempo estimado</span>
                  <span className="font-medium text-gray-900">
                    {estimatedSeconds != null ? formatEstimatedTime(estimatedSeconds) : '—'}
                  </span>
                </div>
              </div>
              <div>
                <p className="text-xs font-medium text-gray-500 uppercase tracking-wider mb-2">Mensagem</p>
                <div className="bg-white rounded-lg border border-gray-200 px-4 py-3 text-sm text-gray-700 whitespace-pre-wrap max-h-32 overflow-y-auto">
                  {state.message}
                </div>
              </div>
              <div className="flex items-start gap-2.5 rounded-lg bg-amber-50 border border-amber-200 px-4 py-3">
                <svg className="h-5 w-5 text-amber-500 mt-0.5 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
                  <path fillRule="evenodd" d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z" clipRule="evenodd" />
                </svg>
                <p className="text-xs text-amber-800 leading-relaxed">
                  <strong>Anti-ban:</strong> envio com intervalo aleatório de 30–90s. O disparo continua em segundo plano.
                </p>
              </div>
              {error && <p className="text-sm text-red-600">{error}</p>}
            </div>
          )}
        </div>

        {/* Footer navigation */}
        <div className="px-6 pb-6 flex justify-between gap-2">
          <button
            onClick={() => step > 1 ? setStep((s) => (s - 1) as WizardStep) : onClose()}
            className="px-5 py-2 rounded-lg bg-gray-100 hover:bg-gray-200 text-gray-700 text-sm font-medium transition-colors"
          >
            {step === 1 ? 'Cancelar' : 'Voltar'}
          </button>
          {step < 3 ? (
            <button
              disabled={
                (step === 1 && !state.selectedSearch) ||
                (step === 2 && (sessions.length === 0 || !state.selectedSessionId || !state.message.trim()))
              }
              onClick={() => setStep((s) => (s + 1) as WizardStep)}
              className="px-5 py-2 rounded-lg bg-blue-600 hover:bg-blue-700 disabled:bg-blue-300 disabled:cursor-not-allowed text-white text-sm font-semibold transition-colors"
            >
              Próximo
            </button>
          ) : (
            <button
              disabled={submitting}
              onClick={handleSubmit}
              className="inline-flex items-center gap-2 px-5 py-2 rounded-lg bg-green-600 hover:bg-green-700 disabled:bg-green-300 disabled:cursor-not-allowed text-white text-sm font-semibold transition-colors"
            >
              {submitting && <Spinner />}
              Iniciar Campanha
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

// ---- Main page ----

export default function CampaignsPage() {
  const [campaigns, setCampaigns] = useState<CampaignSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showWizard, setShowWizard] = useState(false)
  const [detailCampaign, setDetailCampaign] = useState<CampaignSummary | null>(null)
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const fetchCampaigns = useCallback(async () => {
    try {
      const res = await fetch(`${API_URL}/api/v1/campaigns`)
      if (!res.ok) throw new Error(`Erro ${res.status}`)
      const data: CampaignSummary[] = await res.json()
      setCampaigns(data ?? [])
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
    fetchCampaigns()
  }, [fetchCampaigns])

  // Poll while any campaign is not yet COMPLETED
  useEffect(() => {
    const hasActive = campaigns.some((c) => c.status === 'PENDING' || c.status === 'RUNNING')
    if (pollRef.current) {
      clearInterval(pollRef.current)
      pollRef.current = null
    }
    if (hasActive) {
      pollRef.current = setInterval(fetchCampaigns, POLL_MS)
    }
    return () => {
      if (pollRef.current) clearInterval(pollRef.current)
    }
  }, [campaigns, fetchCampaigns])

  const handleCreated = useCallback((c: CampaignSummary) => {
    setShowWizard(false)
    setToast({ message: 'Campanha iniciada com sucesso!', type: 'success' })
    fetchCampaigns()
  }, [fetchCampaigns])

  // Update detail campaign when list is refreshed
  useEffect(() => {
    if (!detailCampaign) return
    const updated = campaigns.find((c) => c.id === detailCampaign.id)
    if (updated) setDetailCampaign(updated)
  }, [campaigns, detailCampaign])

  return (
    <main className="min-h-screen">
      <div className="max-w-6xl mx-auto px-4 sm:px-6 py-10">
        {/* Header */}
        <div className="mb-8 flex flex-col sm:flex-row sm:items-end sm:justify-between gap-4">
          <div>
            <h1 className="text-2xl font-bold text-gray-900">Campanhas</h1>
            <p className="text-gray-500 text-sm mt-1">Gerencie e monitore os disparos de WhatsApp.</p>
          </div>
          <button
            onClick={() => setShowWizard(true)}
            className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-blue-600 hover:bg-blue-700 active:bg-blue-800 text-white text-sm font-semibold transition-colors shadow-sm self-start sm:self-auto"
          >
            <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            Nova Campanha
          </button>
        </div>

        {/* Error banner */}
        {error && (
          <div className="flex items-start gap-3 bg-red-50 border border-red-200 text-red-700 rounded-xl px-5 py-4 mb-6">
            <svg className="h-5 w-5 mt-0.5 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
              <path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z" clipRule="evenodd" />
            </svg>
            <p className="text-sm">{error}</p>
          </div>
        )}

        {/* Campaigns table */}
        <div className="bg-white rounded-2xl shadow-sm border border-gray-100 overflow-hidden">
          {loading ? (
            <div className="flex justify-center py-20 text-gray-400">
              <Spinner className="h-8 w-8" />
            </div>
          ) : campaigns.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 text-center px-4">
              <div className="h-12 w-12 rounded-full bg-gray-100 flex items-center justify-center mb-4">
                <svg className="h-6 w-6 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
                </svg>
              </div>
              <p className="text-gray-500 font-medium">Nenhuma campanha ainda</p>
              <p className="text-gray-400 text-sm mt-1">Clique em &ldquo;Nova Campanha&rdquo; para começar.</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="bg-gray-50 border-b border-gray-100">
                  <tr>
                    {['ID', 'Data', 'Busca de Origem', 'Número Remetente', 'Progresso', 'Status', ''].map((col) => (
                      <th key={col} className="text-left text-xs font-semibold text-gray-500 uppercase tracking-wider px-5 py-3 whitespace-nowrap">
                        {col}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-50">
                  {campaigns.map((c) => (
                    <tr key={c.id} className="hover:bg-gray-50 transition-colors duration-100">
                      <td className="px-5 py-4 font-mono text-xs text-blue-700 whitespace-nowrap">
                        {c.id.slice(0, 8)}…
                      </td>
                      <td className="px-5 py-4 text-gray-500 whitespace-nowrap text-xs">
                        {new Date(c.created_at).toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })}
                      </td>
                      <td className="px-5 py-4 text-gray-800">
                        <p className="font-medium truncate max-w-[160px]">{c.search_niche}</p>
                        <p className="text-xs text-gray-400 truncate max-w-[160px]">{c.search_location}</p>
                      </td>
                      <td className="px-5 py-4 text-gray-600 whitespace-nowrap text-xs">
                        {c.phone_number ? formatPhone(c.phone_number) : '—'}
                      </td>
                      <td className="px-5 py-4 min-w-[140px]">
                        <ProgressBar sent={c.sent} failed={c.failed} total={c.total} />
                      </td>
                      <td className="px-5 py-4 whitespace-nowrap">
                        <StatusBadge status={c.status} />
                      </td>
                      <td className="px-5 py-4">
                        <button
                          onClick={() => setDetailCampaign(c)}
                          className="px-3 py-1.5 rounded-lg text-xs font-medium text-gray-600 hover:text-gray-900 hover:bg-gray-100 transition-colors"
                        >
                          Detalhes
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {showWizard && (
        <NewCampaignWizard
          onClose={() => setShowWizard(false)}
          onCreated={handleCreated}
        />
      )}

      {detailCampaign && (
        <DetailsModal campaign={detailCampaign} onClose={() => setDetailCampaign(null)} />
      )}

      {toast && (
        <Toast
          message={toast.message}
          type={toast.type}
          onDismiss={() => setToast(null)}
        />
      )}
    </main>
  )
}
