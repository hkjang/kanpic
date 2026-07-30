import { useState, type FormEvent } from 'react'
import { ArrowRight, CheckCircle2, Database, LockKeyhole, Sparkles } from 'lucide-react'
import { api } from '../lib/api'
import type { AuthConfig, BuildInfo, Session } from '../types'
import { Brand } from '../components/Brand'

export function LoginPage({build,auth,returnTo='/'}:{build?:BuildInfo;auth?:AuthConfig;returnTo?:string}) {
  const [id,setID]=useState(''),[password,setPassword]=useState(''),[error,setError]=useState(''),[pending,setPending]=useState(false)
  const oidcEnabled=auth?.oidc_enabled===true,bootstrapEnabled=auth?.bootstrap_login_enabled===true
  const login=()=>{window.location.href=`/auth/login?return_to=${encodeURIComponent(returnTo)}`}
  const bootstrapLogin=async(event:FormEvent)=>{event.preventDefault();setError('');setPending(true);try{await api<Session>('/auth/bootstrap/login',{method:'POST',body:JSON.stringify({id,password})});window.location.href=returnTo}catch(reason){setError(reason instanceof Error?reason.message:'로그인하지 못했습니다.');setPending(false)}}
  return <main className="login-page">
    <section className="login-visual">
      <div className="login-orb one"/><div className="login-orb two"/>
      <div className="login-story">
        <Brand/>
        <span className="eyebrow"><Sparkles size={15}/> AI spreadsheet workspace</span>
        <h1>데이터가 모이고,<br/>팀의 판단이 빨라집니다.</h1>
        <p>익숙한 스프레드시트 경험에 실시간 협업, 사내 데이터와 안전한 AI를 하나로 연결하세요.</p>
        <div className="login-benefits"><span><CheckCircle2/> 서버 권위 자동 저장</span><span><CheckCircle2/> 폐쇄망 완전 지원</span><span><CheckCircle2/> API · MCP 우선 설계</span></div>
      </div>
      <div className="login-version">kanpic {build?.version ?? '…'} · {build?.commit && build.commit !== 'unknown' ? build.commit.slice(0,8) : 'development'}</div>
    </section>
    <section className="login-form-wrap">
      <div className="login-form">
        <div className="login-icon"><LockKeyhole/></div>
        <h2>kanpic에 로그인</h2>
        <p>{bootstrapEnabled&&oidcEnabled?'로컬 관리자 또는 조직 계정으로 계속하세요.':bootstrapEnabled?'bootstrap 관리자 계정으로 로그인하세요.':oidcEnabled?'조직 계정으로 안전하게 계속하세요.':'최초 설치 설정을 시작하세요.'}</p>
        {bootstrapEnabled&&<form className="bootstrap-login-form" onSubmit={bootstrapLogin}><label>아이디<input autoFocus autoComplete="username" value={id} onChange={event=>setID(event.target.value)} required/></label><label>비밀번호<input type="password" autoComplete="current-password" value={password} onChange={event=>setPassword(event.target.value)} required/></label>{error&&<div className="login-error" role="alert">{error}</div>}<button className="primary large-button" type="submit" disabled={pending}>{pending?'로그인 중…':'관리자로 로그인'} <ArrowRight size={18}/></button></form>}
        {bootstrapEnabled&&oidcEnabled&&<div className="login-divider"><span>또는</span></div>}
        {oidcEnabled&&<button className="secondary large-button" onClick={login}>조직 계정으로 로그인 <ArrowRight size={18}/></button>}
        {!bootstrapEnabled&&!oidcEnabled&&<a className="primary large-button" href="/admin">로컬 관리자 설정 시작 <ArrowRight size={18}/></a>}
        <div className="login-security"><Database size={17}/><div><strong>데이터는 조직 내부에 유지됩니다</strong><small>Keycloak OIDC와 내부 PostgreSQL을 사용합니다.</small></div></div>
      </div>
      <footer>© {new Date().getFullYear()} kanpic · Private data collaboration</footer>
    </section>
  </main>
}
