/**
 * 값이 숫자로 셈에 들어가는지, 들어간다면 얼마인지 정한다.
 *
 * 이 규칙은 서버의 수식 엔진이 정한다. 화면이 다르게 세면 =SUM(A1:A100) 이
 * 내는 값과 상태 줄에 보이는 합계가 달라지고, 사람은 어느 쪽을 믿어야 할지
 * 알 수 없다. 실제로 그런 일이 있었다 — 선택 요약과 열 통계와 엔진이 셋 다
 * 달랐다.
 *
 *   "2000"    셈한다        엔진도 그렇다
 *   " 300 "   셈한다        앞뒤 빈칸은 버린다
 *   TRUE      1 로 센다     엔진도 그렇다
 *   "1,234"   세지 않는다   쉼표가 붙으면 엔진은 글자로 본다
 *   "50%"     세지 않는다
 *   "1234원"  세지 않는다
 *
 * 쉼표가 붙은 것을 세지 않는 것이 야박해 보이지만, 세는 쪽이 더 나쁘다.
 * 화면에는 더해지는데 수식에서는 빠지므로 두 숫자가 어긋난 채로 남는다.
 * 그런 칸은 세는 대신 찾아서 알려 주고 숫자로 바꿔 주는 편이 낫다.
 */
export function spreadsheetNumber(value:unknown):number|undefined{
  if(typeof value==='number')return Number.isFinite(value)?value:undefined
  if(typeof value==='boolean')return value?1:0
  if(typeof value==='string'){
    const text=value.trim()
    if(text==='')return undefined
    const parsed=Number(text)
    return Number.isFinite(parsed)?parsed:undefined
  }
  return undefined
}

/**
 * 숫자처럼 보이는데 글자로 담긴 칸인지 본다.
 *
 * 다른 곳에서 붙여 넣으면 "1,234" 나 "1,234원" 처럼 들어온다. 사람 눈에는
 * 숫자인데 =SUM 은 조용히 빼고 셈한다. 합계가 작게 나오는데 무엇이 빠졌는지는
 * 아무 데도 적히지 않는다.
 */
export function looksLikeNumberStoredAsText(value:unknown):boolean{
  if(typeof value!=='string')return false
  const text=value.trim()
  if(text==='')return false
  // 이미 셈에 들어가는 것은 고칠 것이 없다.
  if(spreadsheetNumber(text)!==undefined)return false
  return textToNumber(text)!==undefined
}

/**
 * 글자로 담긴 숫자를 숫자로 바꾼다. 바꿀 수 없으면 undefined 다.
 *
 * 쉼표, 원·달러 기호, 뒤에 붙은 단위, 괄호 음수, 백분율을 받는다. 괄호는
 * 회계에서 음수를 적는 꼴이고, 백분율은 100 으로 나눈다 — 화면에 보이던 것과
 * 셈하는 값이 같아야 한다.
 */
export function textToNumber(value:string):number|undefined{
  let text=value.trim()
  if(text==='')return undefined
  let sign=1
  // (500) 은 회계에서 -500 이다.
  if(/^\(.*\)$/.test(text)){sign=-1;text=text.slice(1,-1).trim()}
  const percent=text.endsWith('%')
  if(percent)text=text.slice(0,-1).trim()
  // 앞뒤의 통화 기호와 단위를 떼어 낸다. 가운데 것은 건드리지 않는다 —
  // "1,2,3" 같은 것을 억지로 숫자로 만들면 안 된다.
  text=text.replace(/^[₩$€¥£]\s*/,'').replace(/\s*(원|달러|USD|KRW|won)$/i,'').trim()
  if(text.startsWith('-')){sign*=-1;text=text.slice(1).trim()}
  else if(text.startsWith('+'))text=text.slice(1).trim()
  // 쉼표는 세 자리마다 하나여야 한다. 아무 데나 찍힌 것은 숫자가 아니다.
  if(text.includes(',')){
    if(!/^\d{1,3}(,\d{3})+(\.\d+)?$/.test(text))return undefined
    text=text.replace(/,/g,'')
  }
  if(!/^\d+(\.\d+)?$/.test(text))return undefined
  const parsed=Number(text)
  if(!Number.isFinite(parsed))return undefined
  return sign*(percent?parsed/100:parsed)
}
