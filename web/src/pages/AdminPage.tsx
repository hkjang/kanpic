import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { Activity, ArrowLeft, Building2, Bot, CheckCircle2, ChevronRight, Database, FileClock, KeyRound, ListFilter, Plus, RefreshCw, RotateCcw, Save, Search, ServerCog, Settings2, ShieldCheck, SlidersHorizontal, Trash2, Users, Workflow, XCircle } from 'lucide-react'
import { Brand } from '../components/Brand'
import { ProfileMenu } from '../components/ProfileMenu'
import { api } from '../lib/api'
import type { BuildInfo, Department, DirectoryUser, LogEntry, Session, SettingVersion, SystemSetting } from '../types'

type Tab='settings'|'users'|'departments'|'logs'|'keys'|'system'
const tabFromURL=():Tab=>{const value=new URLSearchParams(location.search).get('tab');return ['users','departments','logs','keys','system'].includes(value||'')?value as Tab:'settings'}

export function AdminPage({build,session}:{build?:BuildInfo;session?:Session}) {
  const [tab,setTab]=useState<Tab>(tabFromURL())
  const navigate=(next:Tab)=>{history.replaceState(null,'',`/admin?tab=${next}`);setTab(next)}
  return <div className="console-shell"><aside className="console-sidebar"><Brand/><div className="console-label">ADMIN CONSOLE</div><nav>
    <button className={tab==='settings'?'active':''} onClick={()=>navigate('settings')}><Settings2/> 시스템 설정 <ChevronRight/></button>
    <button className={tab==='users'?'active':''} onClick={()=>navigate('users')}><Users/> 사용자 및 역할 <ChevronRight/></button>
    <button className={tab==='departments'?'active':''} onClick={()=>navigate('departments')}><Building2/> 부서 및 공유 <ChevronRight/></button>
    <button className={tab==='logs'?'active':''} onClick={()=>navigate('logs')}><Activity/> 서버 로그 <ChevronRight/></button>
    <button className={tab==='keys'?'active':''} onClick={()=>navigate('keys')}><KeyRound/> API 키 현황 <ChevronRight/></button>
    <button className={tab==='system'?'active':''} onClick={()=>navigate('system')}><ServerCog/> 시스템 상태 <ChevronRight/></button>
  </nav><div className="console-nav-group"><span>바로가기</span><a href="/preferences"><ShieldCheck/> 개인 환경설정</a><a href="/"><Database/> 워크스페이스로</a></div><a className="back-link" href="/"><ArrowLeft/> 워크스페이스로</a></aside>
    <div className="console-main"><header className="console-header"><div><span className="status-pill"><i/> 시스템 정상</span></div><ProfileMenu build={build} session={session}/></header>{tab==='settings'&&<SettingsPanel/>}{tab==='users'&&<UsersPanel/>}{tab==='departments'&&<DepartmentsPanel/>}{tab==='logs'&&<LogsPanel/>}{tab==='keys'&&<AdminKeysPanel/>}{tab==='system'&&<SystemPanel build={build}/>}</div>
  </div>
}

/**
 * Identity comes from the identity provider or the bootstrap login, so the
 * console manages what kanpic owns: account status, kanpic roles that
 * role-based sharing can target, notes and active sessions.
 */
function UsersPanel(){
  const client=useQueryClient()
  const [search,setSearch]=useState(''),[selected,setSelected]=useState<string>(),[role,setRole]=useState('')
  const [newUser,setNewUser]=useState({user_id:'',display_name:'',email:''})
  const [message,setMessage]=useState(''),[error,setError]=useState('')
  const users=useQuery({queryKey:['admin-users'],queryFn:()=>api<{items:DirectoryUser[]}>('/api/v1/admin/users')})
  const items=(users.data?.items??[]).filter(user=>{
    const needle=search.trim().toLowerCase()
    if(!needle)return true
    return [user.user_id,user.display_name,user.email,...(user.roles??[]),...(user.departments??[])].filter(Boolean).some(value=>String(value).toLowerCase().includes(needle))
  })
  const current=items.find(user=>user.user_id===selected)??(users.data?.items??[]).find(user=>user.user_id===selected)
  const run=async(action:()=>Promise<unknown>,success:string)=>{
    setError('');setMessage('')
    try{await action();await client.invalidateQueries({queryKey:['admin-users']});setMessage(success)}
    catch(reason){setError(reason instanceof Error?reason.message:'요청을 처리하지 못했습니다.')}
  }
  const create=()=>run(async()=>{
    const created=await api<DirectoryUser>('/api/v1/admin/users',{method:'POST',body:JSON.stringify({...newUser,user_id:newUser.user_id.trim()})})
    setNewUser({user_id:'',display_name:'',email:''});setSelected(created.user_id)
  },'사용자를 등록했습니다.')
  const setStatus=(user:DirectoryUser,status:'active'|'suspended')=>run(
    ()=>api<DirectoryUser>(`/api/v1/admin/users/${encodeURIComponent(user.user_id)}`,{method:'PATCH',body:JSON.stringify({status})}),
    status==='suspended'?'계정을 정지하고 모든 세션을 종료했습니다.':'계정 정지를 해제했습니다.')
  const grant=(user:DirectoryUser)=>{
    const name=role.trim()
    if(!name)return
    void run(async()=>{await api<DirectoryUser>(`/api/v1/admin/users/${encodeURIComponent(user.user_id)}/roles`,{method:'POST',body:JSON.stringify({role:name})});setRole('')},`${name} 역할을 부여했습니다.`)
  }
  const revoke=(user:DirectoryUser,name:string)=>run(
    ()=>api<DirectoryUser>(`/api/v1/admin/users/${encodeURIComponent(user.user_id)}/roles/${encodeURIComponent(name)}`,{method:'DELETE'}),
    `${name} 역할을 회수했습니다.`)
  const signOut=(user:DirectoryUser)=>run(
    ()=>api<{revoked_sessions:number}>(`/api/v1/admin/users/${encodeURIComponent(user.user_id)}/sessions`,{method:'DELETE'}),
    '모든 세션을 종료했습니다.')
  const note=(user:DirectoryUser)=>{
    const next=window.prompt('메모',user.note??'')
    if(next===null)return
    void run(()=>api<DirectoryUser>(`/api/v1/admin/users/${encodeURIComponent(user.user_id)}`,{method:'PATCH',body:JSON.stringify({note:next})}),'메모를 저장했습니다.')
  }
  return <section className="console-panel">
    <header className="panel-header"><div><h1>사용자 및 역할</h1><p>로그인한 사용자는 자동으로 등록됩니다. 계정 정지, kanpic 역할 부여, 세션 종료를 관리하고 역할은 워크북 공유 대상으로 바로 사용할 수 있습니다.</p></div>
      <div className="panel-actions"><Search/><input aria-label="사용자 검색" placeholder="사용자, 이메일, 역할, 부서 검색" value={search} onChange={event=>setSearch(event.target.value)}/></div>
    </header>
    <div className="user-layout">
      <div className="user-table">
        <div className="user-row head"><span>사용자</span><span>역할</span><span>부서</span><span>워크북</span><span>마지막 접속</span></div>
        {users.isLoading?<div className="loading-card">사용자를 불러오는 중…</div>:items.length===0?<div className="empty-state small"><Users/><h3>사용자가 없습니다</h3><p>사용자가 로그인하거나 아래에서 직접 등록하면 표시됩니다.</p></div>:items.map(user=>
          <button className={`user-row${selected===user.user_id?' active':''}${user.status==='suspended'?' suspended':''}`} key={user.user_id} onClick={()=>setSelected(user.user_id)}>
            <span className="user-identity"><strong>{user.display_name||user.user_id}</strong><small>{user.email||user.user_id}</small>{user.status==='suspended'&&<em>정지됨</em>}</span>
            <span className="user-roles">{(user.roles??[]).length===0?<i>—</i>:(user.roles??[]).map(item=><b key={item}>{item}</b>)}</span>
            <span>{(user.departments??[]).join(', ')||'—'}</span>
            <span>{user.owned_workbooks.toLocaleString()}</span>
            <span>{user.last_seen_at?new Date(user.last_seen_at).toLocaleString('ko-KR'):'기록 없음'}</span>
          </button>)}
        <div className="user-create">
          <h3><Plus/> 사용자 등록</h3>
          <input aria-label="사용자 ID" placeholder="사용자 ID 또는 이메일" value={newUser.user_id} onChange={event=>setNewUser(current=>({...current,user_id:event.target.value}))}/>
          <input aria-label="표시 이름" placeholder="표시 이름 (선택)" value={newUser.display_name} onChange={event=>setNewUser(current=>({...current,display_name:event.target.value}))}/>
          <input aria-label="이메일" placeholder="이메일 (선택)" value={newUser.email} onChange={event=>setNewUser(current=>({...current,email:event.target.value}))}/>
          <button className="primary" disabled={!newUser.user_id.trim()} onClick={create}>등록</button>
        </div>
      </div>
      <div className="user-detail">
        {!current?<div className="empty-state small"><ShieldCheck/><h3>사용자를 선택하세요</h3><p>역할 부여, 계정 정지와 세션 종료를 실행할 수 있습니다.</p></div>:<>
          <h2>{current.display_name||current.user_id}</h2>
          <dl className="user-facts">
            <div><dt>사용자 ID</dt><dd>{current.user_id}</dd></div>
            <div><dt>이메일</dt><dd>{current.email||'—'}</dd></div>
            <div><dt>상태</dt><dd className={current.status==='suspended'?'suspended':'active'}>{current.status==='suspended'?'정지됨':'활성'}</dd></div>
            <div><dt>소유 워크북</dt><dd>{current.owned_workbooks.toLocaleString()}개</dd></div>
            <div><dt>부서</dt><dd>{(current.departments??[]).join(', ')||'—'}</dd></div>
            <div><dt>메모</dt><dd>{current.note||'—'}</dd></div>
          </dl>
          <div className="user-roles-editor">
            <h3>kanpic 역할</h3>
            <div className="user-role-chips">{(current.roles??[]).length===0?<span className="user-empty">부여된 역할이 없습니다.</span>:(current.roles??[]).map(item=>
              <span key={item}>{item}<button aria-label={`${item} 역할 회수`} onClick={()=>revoke(current,item)}><XCircle/></button></span>)}</div>
            <div className="user-role-add">
              <input aria-label="부여할 역할" placeholder="예: kanpic-analyst" value={role} onChange={event=>setRole(event.target.value)} onKeyDown={event=>{if(event.key==='Enter')grant(current)}}/>
              <button disabled={!role.trim()} onClick={()=>grant(current)}>역할 부여</button>
            </div>
            <small>역할은 워크북 공유 창에서 ‘역할’ 대상으로 선택할 수 있습니다.</small>
          </div>
          <div className="user-actions">
            <button onClick={()=>note(current)}>메모 편집</button>
            <button onClick={()=>signOut(current)}>모든 세션 종료</button>
            {current.status==='suspended'
              ?<button className="primary" onClick={()=>setStatus(current,'active')}><CheckCircle2/> 정지 해제</button>
              :<button className="danger" onClick={()=>{if(window.confirm(`'${current.user_id}' 계정을 정지하면 즉시 로그아웃되고 모든 요청이 차단됩니다. 계속할까요?`))setStatus(current,'suspended')}}><XCircle/> 계정 정지</button>}
          </div>
        </>}
      </div>
    </div>
    {error&&<div className="panel-error" role="alert">{error}</div>}
    {message&&<div className="panel-message" role="status">{message}</div>}
  </section>
}

/**
 * Departments decide which workbooks a person reaches, so the console keeps the
 * hierarchy, membership and the resulting share reach in one place.
 */
function DepartmentsPanel(){
  const client=useQueryClient()
  const [name,setName]=useState(''),[parentID,setParentID]=useState(''),[description,setDescription]=useState('')
  const [selected,setSelected]=useState<string>(),[member,setMember]=useState(''),[message,setMessage]=useState(''),[error,setError]=useState('')
  const departments=useQuery({queryKey:['departments'],queryFn:()=>api<{items:Department[]}>('/api/v1/departments')})
  const items=departments.data?.items??[]
  const current=items.find(item=>item.id===selected)
  const detail=useQuery({queryKey:['department',selected],queryFn:()=>api<Department>(`/api/v1/departments/${selected}`),enabled:Boolean(selected)})
  const run=async(action:()=>Promise<unknown>,success:string)=>{
    setError('');setMessage('')
    try{await action();await client.invalidateQueries({queryKey:['departments']});if(selected)await client.invalidateQueries({queryKey:['department',selected]});setMessage(success)}
    catch(reason){setError(reason instanceof Error?reason.message:'요청을 처리하지 못했습니다.')}
  }
  const create=()=>run(async()=>{
    const created=await api<Department>('/api/v1/departments',{method:'POST',body:JSON.stringify({name:name.trim(),parent_id:parentID||undefined,description:description.trim()})})
    setName('');setDescription('');setSelected(created.id)
  },'부서를 만들었습니다.')
  const addMember=()=>run(async()=>{
    await api<Department>(`/api/v1/departments/${selected}/members`,{method:'POST',body:JSON.stringify({user_ids:member.split(/[,\s]+/).filter(Boolean)})})
    setMember('')
  },'구성원을 추가했습니다.')
  const removeMember=(userID:string)=>run(()=>api<Department>(`/api/v1/departments/${selected}/members/${encodeURIComponent(userID)}`,{method:'DELETE'}),'구성원을 제거했습니다.')
  const remove=(department:Department)=>{
    if(!window.confirm(`'${department.name}' 부서를 삭제하면 이 부서로 공유된 워크북 권한도 함께 사라집니다. 계속할까요?`))return
    void run(async()=>{await api(`/api/v1/departments/${department.id}`,{method:'DELETE'});if(selected===department.id)setSelected(undefined)},'부서를 삭제했습니다.')
  }
  const rename=(department:Department,next:string)=>{
    const trimmed=next.trim()
    if(!trimmed||trimmed===department.name)return
    void run(()=>api<Department>(`/api/v1/departments/${department.id}`,{method:'PATCH',body:JSON.stringify({name:trimmed,expected_revision:department.revision})}),'부서 이름을 변경했습니다.')
  }
  const move=(department:Department,parent:string)=>void run(()=>api<Department>(`/api/v1/departments/${department.id}`,{method:'PATCH',body:JSON.stringify({parent_id:parent,expected_revision:department.revision})}),'상위 부서를 변경했습니다.')
  return <section className="console-panel">
    <header className="panel-header"><div><h1>부서 및 공유</h1><p>부서를 만들고 구성원을 배치하면 워크북을 부서 단위로 공유할 수 있습니다. 상위 부서에 공유하면 하위 부서 구성원까지 권한을 상속합니다.</p></div></header>
    <div className="department-layout">
      <div className="department-tree">
        <h2>부서 계층 {items.length>0&&<span>{items.length}개</span>}</h2>
        {departments.isLoading?<div className="loading-card">부서를 불러오는 중…</div>:items.length===0?<div className="empty-state small"><Building2/><h3>부서가 없습니다</h3><p>첫 부서를 만들어 조직 구조를 등록하세요.</p></div>:<ul>
          {items.map(item=><li key={item.id} style={{paddingLeft:12+item.depth*16}}>
            <button className={selected===item.id?'active':''} onClick={()=>setSelected(item.id)}><Building2/><span><strong>{item.name}</strong><small>{item.member_count}명{item.description?` · ${item.description}`:''}</small></span></button>
            <button className="department-delete" aria-label={`${item.name} 삭제`} onClick={()=>remove(item)}><Trash2/></button>
          </li>)}
        </ul>}
        <div className="department-create">
          <h3><Plus/> 부서 추가</h3>
          <input aria-label="부서 이름" placeholder="부서 이름" value={name} onChange={event=>setName(event.target.value)}/>
          <select aria-label="상위 부서" value={parentID} onChange={event=>setParentID(event.target.value)}>
            <option value="">최상위 부서</option>
            {items.map(item=><option key={item.id} value={item.id}>{item.path||item.name}</option>)}
          </select>
          <input aria-label="부서 설명" placeholder="설명 (선택)" value={description} onChange={event=>setDescription(event.target.value)}/>
          <button className="primary" disabled={!name.trim()} onClick={create}>부서 만들기</button>
        </div>
      </div>
      <div className="department-detail">
        {!current?<div className="empty-state small"><Users/><h3>부서를 선택하세요</h3><p>구성원을 추가하거나 상위 부서를 변경할 수 있습니다.</p></div>:<>
          <h2>{current.path||current.name}</h2>
          <div className="department-fields">
            <label>부서 이름<input aria-label="선택한 부서 이름" defaultValue={current.name} key={`${current.id}-${current.revision}`} onBlur={event=>rename(current,event.target.value)}/></label>
            <label>상위 부서<select aria-label="선택한 부서의 상위 부서" value={current.parent_id??''} onChange={event=>move(current,event.target.value)}>
              <option value="">최상위 부서</option>
              {items.filter(item=>item.id!==current.id).map(item=><option key={item.id} value={item.id}>{item.path||item.name}</option>)}
            </select></label>
          </div>
          <div className="department-members">
            <h3>구성원 {detail.data?.member_count??current.member_count}명</h3>
            <div className="department-member-add">
              <input aria-label="추가할 구성원" placeholder="사용자 ID 또는 이메일 (쉼표로 여러 명)" value={member} onChange={event=>setMember(event.target.value)} onKeyDown={event=>{if(event.key==='Enter'&&member.trim())addMember()}}/>
              <button disabled={!member.trim()} onClick={addMember}>추가</button>
            </div>
            <ul>
              {(detail.data?.members??[]).map(userID=><li key={userID}><span>{userID}</span><button aria-label={`${userID} 제거`} onClick={()=>removeMember(userID)}><Trash2/></button></li>)}
              {(detail.data?.members??[]).length===0&&<li className="department-empty">아직 구성원이 없습니다.</li>}
            </ul>
          </div>
        </>}
      </div>
    </div>
    {error&&<div className="panel-error" role="alert">{error}</div>}
    {message&&<div className="panel-message" role="status">{message}</div>}
  </section>
}

function SettingsPanel(){
  const client=useQueryClient();const [showAdd,setShowAdd]=useState(false);const [message,setMessage]=useState('')
  const settings=useQuery({queryKey:['admin-settings'],queryFn:()=>api<{items:SystemSetting[]}>('/api/v1/admin/settings')})
  const versions=useQuery({queryKey:['setting-versions'],queryFn:()=>api<{items:SettingVersion[]}>('/api/v1/admin/settings/versions')})
  const save=async(item:SystemSetting,value:unknown)=>{await api(`/api/v1/admin/settings/${encodeURIComponent(item.key)}`,{method:'PUT',body:JSON.stringify({...item,value})});await client.invalidateQueries({queryKey:['admin-settings']});await client.invalidateQueries({queryKey:['setting-versions']})}
  const validate=useMutation({mutationFn:()=>api<{valid:boolean;issues:Array<{key:string;message:string}>}>('/api/v1/admin/settings:validate',{method:'POST',body:'{}'}),onSuccess:r=>setMessage(r.valid?'모든 설정이 유효합니다.':r.issues.map(i=>`${i.key}: ${i.message}`).join('\n'))})
  const test=useMutation({mutationFn:()=>api<{items:Array<{name:string;success:boolean;message:string}>}>('/api/v1/admin/settings:test',{method:'POST',body:'{}'}),onSuccess:r=>setMessage(r.items.map(i=>`${i.success?'✓':'✕'} ${i.name}: ${i.message}`).join('\n'))})
  const byKey=useMemo(()=>new Map(settings.data?.items.map(item=>[item.key,item])),[settings.data])
  const oidcKeys=['auth.oidc.enabled','auth.oidc.issuer_url','auth.oidc.client_id','auth.oidc.client_secret','auth.oidc.scopes','auth.oidc.admin_roles','auth.oidc.ca_pem','server.public_url']
  const aiKeys=['ai.enabled','ai.gateway_url','ai.model','ai.api_key','ai.timeout_seconds','ai.max_input_cells','ai.max_changes','ai.ca_pem']
  const automationKeys=['automation.enabled','automation.max_cells_per_run','automation.max_runs_per_hour','automation.scheduler_poll_seconds']
  return <main className="console-content"><div className="content-title"><div><span className="eyebrow">PLATFORM CONFIGURATION</span><h1>시스템 설정</h1><p>서비스 설정을 변경하고 저장된 버전을 검증·복원합니다.</p></div><button className="primary" onClick={()=>setShowAdd(true)}><Plus/> 설정 추가</button></div>
    {message&&<div className="result-banner"><CheckCircle2/><pre>{message}</pre><button onClick={()=>setMessage('')}>×</button></div>}
    <section className="admin-card oidc-card"><div className="card-heading"><div className="card-icon"><ShieldCheck/></div><div><h2>Keycloak OIDC 간편 연결</h2><p>Public Client는 secret 없이, Confidential Client는 Client Secret과 PKCE로 연결합니다.</p></div><span className={byKey.get('auth.oidc.enabled')?.value?'enabled-badge':'disabled-badge'}>{byKey.get('auth.oidc.enabled')?.value?'사용 중':'사용 안 함'}</span></div>
      <div className="settings-form-grid">{oidcKeys.map(key=>{const item=byKey.get(key);return item?<SettingField key={key} item={item} onSave={value=>save(item,value)}/>:null})}</div>
      <div className="card-actions"><button className="secondary" onClick={()=>validate.mutate()} disabled={validate.isPending}><CheckCircle2/> 설정 검증</button><button className="primary" onClick={()=>test.mutate()} disabled={test.isPending}><RefreshCw className={test.isPending?'spin':''}/> 연결 테스트</button></div>
    </section>
    <section className="admin-card oidc-card"><div className="card-heading"><div className="card-icon"><Bot/></div><div><h2>사내 AI Gateway 간편 연결</h2><p>OpenAI 호환 `/v1` Gateway와 모델을 설정합니다. 선택 범위 데이터는 이 주소로만 전송됩니다.</p></div><span className={byKey.get('ai.enabled')?.value?'enabled-badge':'disabled-badge'}>{byKey.get('ai.enabled')?.value?'사용 중':'사용 안 함'}</span></div>
      <div className="settings-form-grid">{aiKeys.map(key=>{const item=byKey.get(key);return item?<SettingField key={key} item={item} onSave={value=>save(item,value)}/>:null})}</div>
      <div className="card-actions"><button className="secondary" onClick={()=>validate.mutate()} disabled={validate.isPending}><CheckCircle2/> 설정 검증</button><button className="primary" onClick={()=>test.mutate()} disabled={test.isPending}><RefreshCw className={test.isPending?'spin':''}/> Gateway 연결 테스트</button></div>
    </section>
    <section className="admin-card oidc-card"><div className="card-heading"><div className="card-icon"><Workflow/></div><div><h2>워크북 자동화 실행 정책</h2><p>PostgreSQL 작업 로그를 사용하는 수동·셀 변경·Cron 스케줄 자동화의 실행 범위, 시간당 한도와 확인 주기를 설정합니다.</p></div><span className={byKey.get('automation.enabled')?.value?'enabled-badge':'disabled-badge'}>{byKey.get('automation.enabled')?.value?'사용 중':'사용 안 함'}</span></div>
      <div className="settings-form-grid">{automationKeys.map(key=>{const item=byKey.get(key);return item?<SettingField key={key} item={item} onSave={value=>save(item,value)}/>:null})}</div>
      <div className="card-actions"><button className="secondary" onClick={()=>validate.mutate()} disabled={validate.isPending}><CheckCircle2/> 정책 검증</button><button className="primary" onClick={()=>test.mutate()} disabled={test.isPending}><RefreshCw className={test.isPending?'spin':''}/> 저장소 준비 상태 테스트</button></div>
    </section>
    <section className="admin-card"><div className="card-heading compact"><div><h2>전체 설정</h2><p>모든 설정은 키 단위로 추가, 수정, 삭제할 수 있습니다.</p></div><span>{settings.data?.items.length||0}개</span></div><div className="settings-table"><div className="settings-row head"><span>키</span><span>값</span><span>유형</span><span>최종 변경</span><span/></div>{settings.data?.items.map(item=><SettingRow key={item.key} item={item} onSave={value=>save(item,value)} onDelete={async()=>{if(confirm(`${item.key} 설정을 삭제할까요?`)){await api(`/api/v1/admin/settings/${encodeURIComponent(item.key)}`,{method:'DELETE'});client.invalidateQueries({queryKey:['admin-settings']});client.invalidateQueries({queryKey:['setting-versions']})}}}/>)}</div></section>
    <section className="admin-card"><div className="card-heading compact"><div><h2>설정 버전 이력</h2><p>변경할 때마다 전체 설정 스냅샷이 생성됩니다.</p></div><FileClock/></div><div className="version-list">{versions.data?.items.slice(0,12).map(version=><div key={version.revision}><span className="revision">r{version.revision}</span><div><strong>{version.change_summary}</strong><small>{version.actor_id} · {new Date(version.created_at).toLocaleString('ko-KR')}</small></div><button className="ghost" onClick={async()=>{if(confirm(`r${version.revision} 설정으로 복원할까요?`)){await api(`/api/v1/admin/settings/versions/${version.revision}:restore`,{method:'POST',body:'{}'});client.invalidateQueries({queryKey:['admin-settings']});client.invalidateQueries({queryKey:['setting-versions']})}}}><RotateCcw/> 복원</button></div>)}</div></section>
    {showAdd&&<AddSettingModal onClose={()=>setShowAdd(false)} onCreated={()=>{setShowAdd(false);client.invalidateQueries({queryKey:['admin-settings']});client.invalidateQueries({queryKey:['setting-versions']})}}/>}
  </main>
}

function SettingField({item,onSave}:{item:SystemSetting;onSave:(value:unknown)=>Promise<void>}) {
  const [value,setValue]=useState(item.secret&&item.value==null?'':Array.isArray(item.value)?item.value.join(', '):String(item.value??''))
  const [saved,setSaved]=useState(false)
  const commit=async()=>{let parsed:unknown=value;if(item.value_type==='boolean')parsed=value==='true';if(item.value_type==='number')parsed=Number(value);if(item.value_type==='string_list')parsed=value.split(',').map(v=>v.trim()).filter(Boolean);await onSave(parsed);setSaved(true);setTimeout(()=>setSaved(false),1500)}
  const caSetting=item.key.endsWith('.ca_pem')
  const placeholder=item.configured?'저장됨 · 변경할 때만 입력':caSetting?'-----BEGIN CERTIFICATE-----':item.secret?'비밀 값 입력':item.key.includes('issuer')?'https://keycloak.internal/realms/company':item.key==='ai.gateway_url'?'http://vllm.internal:8000/v1':''
  return <label className={caSetting?'wide':''}><span>{item.description||item.key}</span>{item.value_type==='boolean'?<select value={value} onChange={e=>setValue(e.target.value)}><option value="false">사용 안 함</option><option value="true">사용</option></select>:caSetting?<textarea value={value} placeholder={placeholder} onChange={e=>setValue(e.target.value)}/>:<input type={item.secret?'password':'text'} value={value} onChange={e=>setValue(e.target.value)} placeholder={placeholder}/>}<button onClick={commit}>{saved?<CheckCircle2/>:<Save/>}</button><small>{item.key}</small></label>
}
function SettingRow({item,onSave,onDelete}:{item:SystemSetting;onSave:(value:unknown)=>Promise<void>;onDelete:()=>void}){const display=item.secret?(item.configured?'••••••••':'설정 안 됨'):Array.isArray(item.value)?item.value.join(', '):JSON.stringify(item.value);return <div className="settings-row"><div><strong>{item.key}</strong><small>{item.description}</small></div><code>{display}</code><span className="type-badge">{item.value_type}</span><small>{new Date(item.updated_at).toLocaleDateString('ko-KR')}<br/>{item.updated_by}</small><div><button onClick={()=>{const raw=prompt(`${item.key}의 새 값`,String(Array.isArray(item.value)?item.value.join(', '):item.value??''));if(raw==null)return;let value:unknown=raw;if(item.value_type==='boolean')value=raw==='true';if(item.value_type==='number')value=Number(raw);if(item.value_type==='string_list')value=raw.split(',').map(v=>v.trim());if(item.value_type==='object')try{value=JSON.parse(raw)}catch{return alert('올바른 JSON을 입력하세요.')};onSave(value)}}><SlidersHorizontal/></button><button className="danger-icon" onClick={onDelete}><Trash2/></button></div></div>}
function AddSettingModal({onClose,onCreated}:{onClose:()=>void;onCreated:()=>void}){const [key,setKey]=useState(''),[description,setDescription]=useState(''),[type,setType]=useState<SystemSetting['value_type']>('string'),[value,setValue]=useState(''),[secret,setSecret]=useState(false);const create=async()=>{let parsed:unknown=value;if(type==='number')parsed=Number(value);if(type==='boolean')parsed=value==='true';if(type==='string_list')parsed=value.split(',').map(v=>v.trim()).filter(Boolean);if(type==='object')try{parsed=JSON.parse(value)}catch{return alert('올바른 JSON을 입력하세요.')};await api(`/api/v1/admin/settings/${encodeURIComponent(key)}`,{method:'PUT',body:JSON.stringify({key,value:parsed,value_type:type,description,secret})});onCreated()};return <div className="modal-backdrop"><div className="modal"><h2>새 시스템 설정</h2><p>점(.)으로 계층을 구분한 키를 권장합니다.</p><label>키<input value={key} onChange={e=>setKey(e.target.value)} placeholder="category.setting_name"/></label><label>설명<input value={description} onChange={e=>setDescription(e.target.value)}/></label><div className="modal-grid"><label>유형<select value={type} onChange={e=>setType(e.target.value as SystemSetting['value_type'])}><option>string</option><option>number</option><option>boolean</option><option>string_list</option><option>object</option></select></label><label className="check-label"><input type="checkbox" checked={secret} onChange={e=>setSecret(e.target.checked)}/> 비밀 값</label></div><label>값<textarea value={value} onChange={e=>setValue(e.target.value)}/></label><div className="modal-actions"><button className="secondary" onClick={onClose}>취소</button><button className="primary" onClick={create}>추가</button></div></div></div>}

function LogsPanel(){const [level,setLevel]=useState(''),[query,setQuery]=useState('');const logs=useQuery({queryKey:['server-logs',level,query],queryFn:()=>api<{items:LogEntry[]}>(`/api/v1/admin/logs?level=${level}&q=${encodeURIComponent(query)}&limit=200`),refetchInterval:5000});return <main className="console-content"><div className="content-title"><div><span className="eyebrow">OBSERVABILITY</span><h1>서버 로그</h1><p>구조화 로그와 Trace ID로 운영 상태를 추적합니다.</p></div><button className="secondary" onClick={()=>logs.refetch()}><RefreshCw/> 새로고침</button></div><div className="metric-row"><div><small>최근 로그</small><strong>{logs.data?.items.length||0}</strong></div><div><small>오류</small><strong className="error-text">{logs.data?.items.filter(i=>i.level==='ERROR').length||0}</strong></div><div><small>수집 방식</small><strong>PostgreSQL + stdout</strong></div></div><section className="admin-card log-card"><div className="log-filters"><div><Search/><input value={query} onChange={e=>setQuery(e.target.value)} placeholder="메시지, Trace ID 검색"/></div><select value={level} onChange={e=>setLevel(e.target.value)}><option value="">전체 레벨</option><option>INFO</option><option>WARN</option><option>ERROR</option></select><button><ListFilter/> 필터</button></div><div className="log-table"><div className="log-row head"><span>시각</span><span>레벨</span><span>메시지</span><span>속성</span><span>Trace ID</span></div>{logs.data?.items.map(log=><div className="log-row" key={log.id}><time>{new Date(log.logged_at).toLocaleString('ko-KR')}</time><span className={`level ${log.level.toLowerCase()}`}>{log.level}</span><strong>{log.message}</strong><code>{JSON.stringify(log.attributes)}</code><code>{log.trace_id?.slice(0,12)}</code></div>)}</div></section></main>}
function AdminKeysPanel(){return <main className="console-content"><div className="content-title"><div><span className="eyebrow">ACCESS CONTROL</span><h1>API 키 현황</h1><p>개인 키의 소유자, scope, 만료 및 폐기 상태를 감사합니다.</p></div></div><AdminKeyTable/></main>}
function AdminKeyTable(){const keys=useQuery({queryKey:['admin-api-keys'],queryFn:()=>api<{items:Array<{id:string;user_id:string;name:string;prefix:string;scopes:string[];revoked_at?:string;last_used_at?:string}>}>('/api/v1/admin/api-keys')});return <section className="admin-card"><div className="settings-table"><div className="settings-row head"><span>소유자 / 이름</span><span>Prefix</span><span>Scope</span><span>최근 사용</span><span>상태</span></div>{keys.data?.items.map(key=><div className="settings-row" key={key.id}><div><strong>{key.name}</strong><small>{key.user_id}</small></div><code>{key.prefix}…</code><span className="scope-list">{key.scopes.join(', ')}</span><small>{key.last_used_at?new Date(key.last_used_at).toLocaleString('ko-KR'):'사용 전'}</small><span className={key.revoked_at?'disabled-badge':'enabled-badge'}>{key.revoked_at?'폐기됨':'활성'}</span></div>)}</div></section>}
function SystemPanel({build}:{build?:BuildInfo}){const health=useQuery({queryKey:['health'],queryFn:()=>api<{status:string;service:string}>('/healthz'),refetchInterval:10000});return <main className="console-content"><div className="content-title"><div><span className="eyebrow">SYSTEM STATUS</span><h1>시스템 상태</h1><p>초기 모놀리스 서비스와 데이터 저장소 상태입니다.</p></div></div><div className="service-grid"><div className="service-card"><CheckCircle2/><div><strong>kanpic API</strong><small>{health.data?.status||'확인 중'} · {build?.version}</small></div></div><div className="service-card"><CheckCircle2/><div><strong>PostgreSQL</strong><small>연결됨 · 서버 권위 저장소</small></div></div><div className="service-card muted"><XCircle/><div><strong>Redis</strong><small>초기 버전에서 사용하지 않음</small></div></div></div></main>}
