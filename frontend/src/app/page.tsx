'use client'

import { useState, useEffect, useRef, useCallback } from 'react'
import CampaignModal from '@/components/CampaignModal'

interface Company {
  id: string
  search_id: string
  name: string
  location: string
  phone: string
  website: string
  created_at: string
}

interface SearchDetail {
  search_id: string
  status: 'PENDING' | 'RUNNING' | 'COMPLETED' | 'FAILED'
  niche: string
  location: string
  quantity: number
  progress: number
  error_msg?: string
  started_at?: string
  completed_at?: string
  estimated_seconds_remaining?: number
  created_at: string
  companies: Company[]
}

interface SearchSummary {
  id: string
  niche: string
  location: string
  quantity: number
  status: string
  progress: number
  created_at: string
}

const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'
const POLL_INTERVAL_MS = 3000

// ── Helpers ──────────────────────────────────────────────────────────────────

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)}s`
  const m = Math.floor(seconds / 60)
  const s = Math.round(seconds % 60)
  return s > 0 ? `${m}min ${s}s` : `${m}min`
}

// ── Shared UI components ──────────────────────────────────────────────────────

function Spinner({ size = 'sm' }: { size?: 'sm' | 'md' }) {
  const cls = size === 'md' ? 'h-6 w-6' : 'h-4 w-4'
  return (
    <svg className={`animate-spin ${cls}`} xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
    </svg>
  )
}

function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-3 bg-red-50 border border-red-200 text-red-700 rounded-xl px-5 py-4 mb-6">
      <svg className="h-5 w-5 mt-0.5 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
        <path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z" clipRule="evenodd" />
      </svg>
      <p className="text-sm">{message}</p>
    </div>
  )
}

// ── Progress panel shown while a search is PENDING or RUNNING ─────────────────

function SearchProgress({ detail }: { detail: SearchDetail }) {
  const pct = detail.quantity > 0 ? Math.min(100, Math.round((detail.progress / detail.quantity) * 100)) : 0
  const est = detail.estimated_seconds_remaining

  const statusLabel =
    detail.status === 'PENDING' ? 'Aguardando início…' :
    detail.status === 'RUNNING' ? 'Buscando empresas…' :
    detail.status === 'FAILED'  ? 'Falha na busca' : 'Concluído'

  const barColor =
    detail.status === 'FAILED' ? 'bg-red-500' :
    detail.status === 'COMPLETED' ? 'bg-green-500' : 'bg-blue-500'

  return (
    <div className="bg-white rounded-2xl shadow-sm border border-gray-100 p-6 mb-6">
      <div className="flex items-center gap-3 mb-4">
        {(detail.status === 'PENDING' || detail.status === 'RUNNING') && (
          <Spinner size="md" />
        )}
        {detail.status === 'COMPLETED' && (
          <div className="h-6 w-6 rounded-full bg-green-100 flex items-center justify-center flex-shrink-0">
            <svg className="h-4 w-4 text-green-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
            </svg>
          </div>
        )}
        {detail.status === 'FAILED' && (
          <div className="h-6 w-6 rounded-full bg-red-100 flex items-center justify-center flex-shrink-0">
            <svg className="h-4 w-4 text-red-600" fill="currentColor" viewBox="0 0 20 20">
              <path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z" clipRule="evenodd" />
            </svg>
          </div>
        )}
        <div>
          <p className="font-semibold text-gray-900">{statusLabel}</p>
          <p className="text-xs text-gray-500">{detail.niche} · {detail.location}</p>
        </div>
      </div>

      {/* Progress bar */}
      <div className="mb-3">
        <div className="flex justify-between text-xs text-gray-500 mb-1.5">
          <span>
            {detail.progress.toLocaleString('pt-BR')} / {detail.quantity.toLocaleString('pt-BR')} empresas
          </span>
          <span>{pct}%</span>
        </div>
        <div className="w-full bg-gray-100 rounded-full h-2.5 overflow-hidden">
          <div
            className={`${barColor} h-2.5 rounded-full transition-all duration-700`}
            style={{ width: `${pct}%` }}
          />
        </div>
      </div>

      {/* Time estimate */}
      {detail.status === 'RUNNING' && (
        <div className="flex flex-wrap gap-4 text-xs text-gray-400">
          {est != null && est > 0 && (
            <span>⏱ Tempo estimado restante: <strong className="text-gray-600">{formatDuration(est)}</strong></span>
          )}
          {detail.started_at && (
            <span>
              Iniciado há{' '}
              <strong className="text-gray-600">
                {formatDuration((Date.now() - new Date(detail.started_at).getTime()) / 1000)}
              </strong>
            </span>
          )}
        </div>
      )}

      {detail.status === 'FAILED' && detail.error_msg && (
        <p className="text-xs text-red-600 mt-2">{detail.error_msg}</p>
      )}
    </div>
  )
}

// ── Sidebar recent searches ──────────────────────────────────────────────────

function StatusDot({ status }: { status: string }) {
  const color =
    status === 'COMPLETED' ? 'bg-green-400' :
    status === 'RUNNING'   ? 'bg-blue-400 animate-pulse' :
    status === 'FAILED'    ? 'bg-red-400' :
    'bg-gray-300'
  return <span className={`inline-block h-2 w-2 rounded-full ${color} flex-shrink-0`} />
}

function RecentSearches({
  searches,
  loading,
  activeId,
  onSelect,
  onStartCampaign,
}: {
  searches: SearchSummary[]
  loading: boolean
  activeId: string | null
  onSelect: (id: string) => void
  onStartCampaign: (s: SearchSummary) => void
}) {
  return (
    <div className="surface p-4 sm:p-5">
      <h2 className="text-xs font-bold text-gray-500 uppercase tracking-[0.14em] mb-3">
        Histórico recente
      </h2>
      {loading ? (
        <div className="flex justify-center py-6"><Spinner /></div>
      ) : searches.length === 0 ? (
        <p className="text-xs text-gray-400 text-center py-6">Nenhuma busca realizada ainda.</p>
      ) : (
        <ul className="space-y-1">
          {searches.map((s) => (
            <li key={s.id} className="relative">
              <button
                onClick={() => onSelect(s.id)}
                className={`w-full text-left pl-3 pr-10 py-2.5 rounded-lg transition-colors duration-100 ${
                  activeId === s.id
                    ? 'bg-blue-50 border border-blue-200'
                    : 'hover:bg-gray-50 border border-transparent'
                }`}
              >
                <div className="flex items-center gap-2 mb-0.5">
                  <StatusDot status={s.status} />
                  <p className="text-sm font-medium text-gray-800 truncate">{s.niche}</p>
                </div>
                <p className="text-xs text-gray-500 truncate pl-4">{s.location}</p>
                <p className="text-xs text-gray-400 mt-0.5 pl-4">
                  {s.status === 'RUNNING' && s.progress > 0
                    ? `${s.progress.toLocaleString('pt-BR')} / ${s.quantity.toLocaleString('pt-BR')} empresas`
                    : new Date(s.created_at).toLocaleDateString('pt-BR', {
                        day: '2-digit', month: '2-digit', year: 'numeric',
                        hour: '2-digit', minute: '2-digit',
                      })}
                </p>
              </button>
              <button
                onClick={() => onStartCampaign(s)}
                title="Iniciar Campanha"
                aria-label={`Iniciar campanha para ${s.niche}`}
                className="absolute top-2 right-2 p-1.5 rounded-md text-gray-400 hover:text-green-600 hover:bg-green-50 transition-colors"
              >
                <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
                </svg>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// ── Main dashboard ────────────────────────────────────────────────────────────

export default function Dashboard() {
  const [niche, setNiche] = useState('')
  const [location, setLocation] = useState('')
  const [quantity, setQuantity] = useState(10)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Active search being polled or displayed
  const [activeDetail, setActiveDetail] = useState<SearchDetail | null>(null)
  const [activeSearchId, setActiveSearchId] = useState<string | null>(null)

  const [recentSearches, setRecentSearches] = useState<SearchSummary[]>([])
  const [loadingHistory, setLoadingHistory] = useState(false)

  const [campaignTarget, setCampaignTarget] = useState<{ id: string; label: string } | null>(null)

  // Ref keeps the polling interval stable without re-creating effects
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const stopPolling = useCallback(() => {
    if (pollRef.current !== null) {
      clearInterval(pollRef.current)
      pollRef.current = null
    }
  }, [])

  // Poll a search by ID and update activeDetail.
  const pollSearch = useCallback(async (id: string) => {
    try {
      const res = await fetch(`${API_URL}/api/v1/searches/${id}`)
      if (!res.ok) return
      const data: SearchDetail = await res.json()
      setActiveDetail(data)

      if (data.status === 'COMPLETED' || data.status === 'FAILED') {
        stopPolling()
        fetchHistory()
      }
    } catch {
      // transient network error — keep polling
    }
  }, [stopPolling])

  const startPolling = useCallback((id: string) => {
    stopPolling()
    pollSearch(id) // immediate first fetch
    pollRef.current = setInterval(() => pollSearch(id), POLL_INTERVAL_MS)
  }, [pollSearch, stopPolling])

  // Cleanup on unmount
  useEffect(() => () => stopPolling(), [stopPolling])

  useEffect(() => { fetchHistory() }, [])

  async function fetchHistory() {
    setLoadingHistory(true)
    try {
      const res = await fetch(`${API_URL}/api/v1/searches`)
      if (res.ok) {
        const data: SearchSummary[] = await res.json()
        setRecentSearches(data ?? [])
      }
    } catch {
      // history is non-critical; silently ignore
    } finally {
      setLoadingHistory(false)
    }
  }

  async function loadSearch(id: string) {
    setError(null)
    setActiveSearchId(id)
    stopPolling()

    try {
      const res = await fetch(`${API_URL}/api/v1/searches/${id}`)
      if (!res.ok) {
        const body = await res.json().catch(() => null)
        throw new Error(body?.error || `Erro ${res.status}: ${res.statusText}`)
      }
      const data: SearchDetail = await res.json()
      setActiveDetail(data)

      // Resume polling if the search is still in progress
      if (data.status === 'PENDING' || data.status === 'RUNNING') {
        startPolling(id)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Erro ao carregar busca.')
    }
  }

  async function handleSearch(e: React.FormEvent) {
    e.preventDefault()
    setSubmitting(true)
    setError(null)
    setActiveDetail(null)
    setActiveSearchId(null)
    stopPolling()

    try {
      const res = await fetch(`${API_URL}/api/v1/searches`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ niche, location, quantity: Number(quantity) }),
      })

      if (!res.ok) {
        const body = await res.json().catch(() => null)
        throw new Error(body?.error || `Erro ${res.status}: ${res.statusText}`)
      }

      const { search_id } = await res.json() as { search_id: string }
      setActiveSearchId(search_id)
      fetchHistory()
      startPolling(search_id)
    } catch (err) {
      if (err instanceof TypeError) {
        setError('Não foi possível conectar à API. Verifique se o servidor está rodando em ' + API_URL)
      } else {
        setError(err instanceof Error ? err.message : 'Ocorreu um erro inesperado.')
      }
    } finally {
      setSubmitting(false)
    }
  }

  const isInProgress = activeDetail?.status === 'PENDING' || activeDetail?.status === 'RUNNING'
  const isCompleted  = activeDetail?.status === 'COMPLETED'
  const hasManyResults = isCompleted && (activeDetail?.companies?.length ?? 0) > 0

  return (
    <main className="min-h-screen">
      <div className="page-shell">

        {/* Header */}
        <div className="page-heading">
          <div>
            <p className="eyebrow">Inteligência comercial</p>
            <h1 className="page-title">Encontre as empresas certas.</h1>
            <p className="page-lead">Pesquise por segmento e região, acompanhe a coleta em tempo real e organize os resultados em um só lugar.</p>
          </div>
          <div className="hidden sm:flex items-center gap-2 rounded-2xl border border-blue-100 bg-blue-50/80 px-4 py-3 text-xs font-semibold text-blue-700">
            <span className="h-2 w-2 rounded-full bg-blue-500 animate-pulse" />
            Busca assistida ativa
          </div>
        </div>

        {/* Layout: sidebar + main */}
        <div className="flex flex-col lg:flex-row lg:gap-7 lg:items-start">

          {/* Sidebar */}
          <aside className="w-full lg:w-72 lg:flex-shrink-0 order-2 lg:order-1 mt-5 lg:mt-0">
            <RecentSearches
              searches={recentSearches}
              loading={loadingHistory}
              activeId={activeSearchId}
              onSelect={loadSearch}
              onStartCampaign={(s) => setCampaignTarget({ id: s.id, label: `${s.niche} · ${s.location}` })}
            />
          </aside>

          {/* Main */}
          <div className="flex-1 min-w-0 order-1 lg:order-2">

            {/* Search form */}
            <div className="surface p-4 sm:p-6 mb-5">
              <div className="flex items-center justify-between gap-3 mb-5">
                <div>
                  <p className="eyebrow">Nova pesquisa</p>
                  <h2 className="text-lg font-semibold text-gray-900">Defina seu mercado</h2>
                </div>
                <span className="hidden sm:inline text-xs font-medium text-gray-400">Até 5.000 resultados</span>
              </div>
              <form onSubmit={handleSearch} className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-[1fr_1fr_9rem_auto] xl:items-end">
                <div className="flex-1 min-w-0">
                  <label htmlFor="niche" className="block text-sm font-medium text-gray-700 mb-1.5">Nicho</label>
                  <input
                    id="niche" type="text"
                    placeholder="Ex: Padarias, Oficinas, Clínicas..."
                    value={niche} onChange={(e) => setNiche(e.target.value)} required
                    className="w-full px-4 py-3 rounded-xl border border-gray-200 bg-gray-50 focus:bg-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent text-gray-900 placeholder-gray-400 transition-colors text-sm"
                  />
                </div>

                <div className="flex-1 min-w-0">
                  <label htmlFor="location" className="block text-sm font-medium text-gray-700 mb-1.5">Cidade / Estado / País</label>
                  <input
                    id="location" type="text"
                    placeholder="Ex: São Paulo, SP, Brasil"
                    value={location} onChange={(e) => setLocation(e.target.value)} required
                    className="w-full px-4 py-3 rounded-xl border border-gray-200 bg-gray-50 focus:bg-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent text-gray-900 placeholder-gray-400 transition-colors text-sm"
                  />
                </div>

                <div className="w-full">
                  <label htmlFor="quantity" className="block text-sm font-medium text-gray-700 mb-1.5">
                    Quantidade <span className="text-gray-400 font-normal">(máx 5000)</span>
                  </label>
                  <input
                    id="quantity" type="number"
                    min={1} max={5000}
                    value={quantity} onChange={(e) => setQuantity(Number(e.target.value))} required
                    className="w-full px-4 py-3 rounded-xl border border-gray-200 bg-gray-50 focus:bg-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent text-gray-900 transition-colors text-sm"
                  />
                </div>

                <button
                  type="submit"
                  disabled={submitting || isInProgress}
                  className="w-full flex-shrink-0 px-6 py-3 bg-blue-600 hover:bg-blue-700 active:bg-blue-800 disabled:bg-blue-300 disabled:cursor-not-allowed text-white font-semibold rounded-xl transition-colors duration-150 flex items-center justify-center gap-2 text-sm whitespace-nowrap md:col-span-2 xl:col-span-1"
                >
                  {submitting ? (
                    <><Spinner /> Iniciando…</>
                  ) : (
                    <>
                      <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-4.35-4.35M17 11A6 6 0 1 0 5 11a6 6 0 0 0 12 0z" />
                      </svg>
                      Buscar Empresas
                    </>
                  )}
                </button>
              </form>

              {/* Hint for large searches */}
              {quantity >= 500 && (
                <p className="mt-3 text-xs text-amber-600 bg-amber-50 border border-amber-200 rounded-lg px-3 py-2">
                  Buscas acima de 500 empresas podem levar vários minutos. O progresso será exibido em tempo real.
                </p>
              )}
            </div>

            {/* Error */}
            {error && <ErrorBanner message={error} />}

            {/* In-progress panel */}
            {activeDetail && isInProgress && (
              <SearchProgress detail={activeDetail} />
            )}

            {/* Failure panel */}
            {activeDetail?.status === 'FAILED' && (
              <SearchProgress detail={activeDetail} />
            )}

            {/* Results */}
            {isCompleted && activeDetail && (
              <div className="surface overflow-hidden">
                <div className="px-6 py-4 border-b border-gray-100 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
                  <div>
                    <h2 className="text-base font-semibold text-gray-900">
                      {activeDetail.companies.length.toLocaleString('pt-BR')}{' '}
                      empresa{activeDetail.companies.length !== 1 ? 's' : ''}{' '}
                      encontrada{activeDetail.companies.length !== 1 ? 's' : ''}
                    </h2>
                    <p className="text-xs text-gray-400 mt-0.5">
                      {activeDetail.niche} &middot; {activeDetail.location}
                      {activeDetail.started_at && activeDetail.completed_at && (
                        <> &middot; {formatDuration((new Date(activeDetail.completed_at).getTime() - new Date(activeDetail.started_at).getTime()) / 1000)}</>
                      )}
                    </p>
                  </div>

                  <div className="flex flex-col gap-2 sm:items-end">
                    <div className="inline-flex items-center gap-2 bg-gray-50 border border-gray-200 rounded-lg px-3 py-2 self-start sm:self-auto">
                      <span className="text-xs text-gray-500 font-medium whitespace-nowrap">ID de Execução</span>
                      <code className="text-xs font-mono text-blue-700 bg-blue-50 px-2 py-0.5 rounded break-all">
                        {activeDetail.search_id}
                      </code>
                    </div>
                    {hasManyResults && (
                      <button
                        onClick={() => setCampaignTarget({ id: activeDetail.search_id, label: `${activeDetail.niche} · ${activeDetail.location}` })}
                        className="inline-flex items-center gap-2 self-start sm:self-auto px-4 py-2 rounded-lg bg-green-600 hover:bg-green-700 active:bg-green-800 text-white text-sm font-semibold transition-colors"
                      >
                        <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
                        </svg>
                        Criar campanha
                      </button>
                    )}
                  </div>
                </div>

                {activeDetail.companies.length === 0 ? (
                  <p className="px-6 py-8 text-center text-gray-400 text-sm">
                    Nenhuma empresa encontrada para os critérios informados.
                  </p>
                ) : (
                  <div className="desktop-table overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead className="bg-gray-50">
                        <tr>
                          {['Nome', 'Localização', 'Telefone', 'Site'].map((col) => (
                            <th key={col} className="text-left text-xs font-semibold text-gray-500 uppercase tracking-wider px-6 py-3">
                              {col}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-gray-50">
                        {activeDetail.companies.map((company) => (
                          <tr key={company.id} className="hover:bg-gray-50 transition-colors duration-100">
                            <td className="px-6 py-3.5 font-medium text-gray-900 whitespace-nowrap">
                              {company.name || <span className="text-gray-400">—</span>}
                            </td>
                            <td className="px-6 py-3.5 text-gray-600">
                              {company.location || <span className="text-gray-400">—</span>}
                            </td>
                            <td className="px-6 py-3.5 text-gray-600 whitespace-nowrap">
                              {company.phone || <span className="text-gray-400">—</span>}
                            </td>
                            <td className="px-6 py-3.5">
                              {company.website ? (
                                <a href={company.website} target="_blank" rel="noopener noreferrer"
                                   className="text-blue-600 hover:text-blue-800 hover:underline max-w-[220px] truncate inline-block align-bottom"
                                   title={company.website}>
                                  {company.website.replace(/^https?:\/\//, '').replace(/\/$/, '')}
                                </a>
                              ) : (
                                <span className="text-gray-400">—</span>
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}

                {activeDetail.companies.length > 0 && (
                  <div className="mobile-card-list">
                    {activeDetail.companies.map((company) => (
                      <article key={`mobile-${company.id}`} className="rounded-2xl border border-gray-100 bg-white p-4 shadow-sm">
                        <div className="flex items-start justify-between gap-3">
                          <div className="min-w-0">
                            <h3 className="truncate text-sm font-semibold text-gray-900">{company.name || 'Empresa sem nome'}</h3>
                            <p className="mt-1 text-xs leading-relaxed text-gray-500">{company.location || 'Localização não informada'}</p>
                          </div>
                          <span className="rounded-full bg-blue-50 px-2 py-1 text-[10px] font-bold uppercase tracking-wide text-blue-700">Lead</span>
                        </div>
                        <div className="mt-4 grid gap-2 text-xs">
                          <a href={company.phone ? `tel:${company.phone.replace(/\D/g, '')}` : undefined} className="font-medium text-gray-700">
                            {company.phone || 'Telefone não informado'}
                          </a>
                          {company.website && (
                            <a href={company.website} target="_blank" rel="noopener noreferrer" className="truncate font-semibold text-blue-600">
                              {company.website.replace(/^https?:\/\//, '').replace(/\/$/, '')}
                            </a>
                          )}
                        </div>
                      </article>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      {campaignTarget && (
        <CampaignModal
          searchId={campaignTarget.id}
          searchLabel={campaignTarget.label}
          onClose={() => setCampaignTarget(null)}
        />
      )}
    </main>
  )
}
