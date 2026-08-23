/**
 * 예시로 규칙을 알아내 나머지를 채운다.
 *
 * 사람이 한두 줄을 손으로 채워 놓으면 그것을 보고 나머지 줄에 같은 일을 한다.
 * 이름에서 성만 떼거나, 주소에서 도메인만 떼거나, 두 열을 붙이는 일 —
 * 스프레드시트에서 손으로 가장 많이 반복되는 일이다.
 *
 * 규칙은 **예시를 하나도 빠짐없이 설명할 때만** 인정한다. 대충 들어맞는
 * 규칙으로 수백 줄을 채우면, 어디가 틀렸는지 아무도 모르는 표가 된다.
 */

export type Extract=
  |{kind:'whole';column:number}
  |{kind:'prefix';column:number;length:number}
  |{kind:'suffix';column:number;length:number}
  |{kind:'before';column:number;delimiter:string}
  |{kind:'after';column:number;delimiter:string}
  |{kind:'beforeLast';column:number;delimiter:string}
  |{kind:'afterLast';column:number;delimiter:string}
  |{kind:'between';column:number;left:string;right:string}

export type Transform='none'|'upper'|'lower'

export type Part={literal:string}|{extract:Extract;transform:Transform}

export type FillRule={parts:Part[]}

export type FillExample={sources:string[];output:string}

/** 규칙을 한 줄에 적용한다. */
export function applyRule(rule:FillRule,sources:string[]):string{
  return rule.parts.map(part=>{
    if('literal' in part)return part.literal
    const text=extract(part.extract,sources)
    if(text===undefined)return ''
    return part.transform==='upper'?text.toUpperCase():part.transform==='lower'?text.toLowerCase():text
  }).join('')
}

function extract(rule:Extract,sources:string[]):string|undefined{
  const source=sources[rule.column]
  if(source===undefined)return undefined
  switch(rule.kind){
    case 'whole':return source
    case 'prefix':return source.slice(0,rule.length)
    case 'suffix':return rule.length>=source.length?source:source.slice(source.length-rule.length)
    case 'before':{const at=source.indexOf(rule.delimiter);return at<0?undefined:source.slice(0,at)}
    case 'after':{const at=source.indexOf(rule.delimiter);return at<0?undefined:source.slice(at+rule.delimiter.length)}
    case 'beforeLast':{const at=source.lastIndexOf(rule.delimiter);return at<0?undefined:source.slice(0,at)}
    case 'afterLast':{const at=source.lastIndexOf(rule.delimiter);return at<0?undefined:source.slice(at+rule.delimiter.length)}
    case 'between':{
      const from=source.indexOf(rule.left)
      if(from<0)return undefined
      const start=from+rule.left.length
      const to=source.indexOf(rule.right,start)
      return to<0?undefined:source.slice(start,to)
    }
  }
}

/**
 * 예시들을 보고 규칙을 찾는다. 모든 예시를 정확히 재현하는 규칙만 돌려준다.
 */
export function inferRule(examples:FillExample[]):FillRule|undefined{
  const usable=examples.filter(example=>example.output.trim()!=='')
  if(usable.length===0)return undefined
  for(const candidate of candidateRules(usable[0])){
    if(usable.every(example=>applyRule(candidate,example.sources)===example.output))return candidate
  }
  return undefined
}

const MAX_CANDIDATES=400

/** 첫 예시를 조각내고, 조각마다 후보 규칙을 만들어 조합한다. */
function* candidateRules(example:FillExample):Generator<FillRule>{
  const segments=segment(example.output,example.sources)
  if(!segments)return
  const choices=segments.map(part=>'literal' in part?[part]:partCandidates(part,example.sources))
  let produced=0
  for(const combination of combine(choices)){
    if(produced++>=MAX_CANDIDATES)return
    yield {parts:combination}
  }
}

function* combine(choices:Part[][]):Generator<Part[]>{
  if(choices.length===0){yield [];return}
  const [first,...rest]=choices
  for(const head of first)
    for(const tail of combine(rest))yield [head,...tail]
}

type Slice={column:number;start:number;length:number;transform:Transform}

/**
 * 출력을 원본에서 온 조각과 그 사이의 글자로 나눈다. 원본에서 찾을 수 있는
 * 가장 긴 조각을 먼저 집는다.
 */
function segment(output:string,sources:string[]):Array<{literal:string}|Slice>|undefined{
  const parts:Array<{literal:string}|Slice>=[]
  let literal=''
  let at=0
  let guard=0
  while(at<output.length){
    if(guard++>output.length*4)return undefined
    const found=longestMatch(output,at,sources)
    // 한 글자짜리 조각은 우연히 맞은 것일 수 있다. 다만 그 한 글자가 원본
    // 칸의 처음이나 끝이면 우연이 아니다 — 한국 성씨는 대개 한 글자다.
    const anchored=found&&(found.start===0||found.start+found.length===sourceLength(sources,found))
    if(found&&(found.length>1||anchored)){
      if(literal){parts.push({literal});literal=''}
      parts.push(found)
      at+=found.length
      continue
    }
    literal+=output[at]
    at+=1
  }
  if(literal)parts.push({literal})
  return parts.some(part=>!('literal' in part))?parts:undefined
}

function sourceLength(sources:string[],slice:Slice){return (sources[slice.column]??'').length}

function longestMatch(output:string,at:number,sources:string[]):Slice|undefined{
  let best:Slice|undefined
  for(let column=0;column<sources.length;column+=1){
    const source=sources[column]
    if(!source)continue
    for(const transform of ['none','upper','lower'] as Transform[]){
      const shaped=transform==='upper'?source.toUpperCase():transform==='lower'?source.toLowerCase():source
      let length=Math.min(shaped.length,output.length-at)
      while(length>0){
        const piece=output.slice(at,at+length)
        const start=shaped.indexOf(piece)
        if(start>=0){
          if(!best||length>best.length)best={column,start,length,transform}
          break
        }
        length-=1
      }
    }
  }
  return best
}

/** 한 조각을 만들어 내는 규칙 후보들. 일반적인 것부터 늘어놓는다. */
function partCandidates(slice:Slice,sources:string[]):Part[]{
  const source=sources[slice.column]??''
  const shaped=slice.transform==='upper'?source.toUpperCase():slice.transform==='lower'?source.toLowerCase():source
  const end=slice.start+slice.length
  const candidates:Extract[]=[]
  if(slice.start===0&&slice.length===shaped.length)candidates.push({kind:'whole',column:slice.column})
  // 구분자로 자르는 규칙이 자리로 자르는 규칙보다 다른 줄에서도 맞을 확률이 높다.
  if(slice.start===0&&end<shaped.length){
    const delimiter=source[end]
    if(delimiter){
      candidates.push({kind:'before',column:slice.column,delimiter})
      candidates.push({kind:'beforeLast',column:slice.column,delimiter})
    }
  }
  if(end===shaped.length&&slice.start>0){
    const delimiter=source[slice.start-1]
    if(delimiter){
      candidates.push({kind:'afterLast',column:slice.column,delimiter})
      candidates.push({kind:'after',column:slice.column,delimiter})
    }
  }
  // 가운데 조각은 앞뒤 구분자 사이의 것으로 읽는다 — 날짜에서 달만 떼는 일.
  if(slice.start>0&&end<shaped.length){
    const left=source[slice.start-1],right=source[end]
    if(left&&right)candidates.push({kind:'between',column:slice.column,left,right})
  }
  if(slice.start===0)candidates.push({kind:'prefix',column:slice.column,length:slice.length})
  if(end===shaped.length)candidates.push({kind:'suffix',column:slice.column,length:slice.length})
  return candidates.map(rule=>({extract:rule,transform:slice.transform}))
}
