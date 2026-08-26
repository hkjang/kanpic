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
  // 배정밀도가 정확히 담는 것은 열다섯 자리까지다. 그보다 긴 것은 계좌번호나
  // 전표번호이지 금액이 아니다. 숫자로 바꾸면 뒤가 뭉개져 사람이 적은 것과
  // 다른 값이 칸에 들어간다.
  if(significantDigits(text)>15)return undefined
  const parsed=Number(text)
  if(!Number.isFinite(parsed))return undefined
  return sign*(percent?parsed/100:parsed)
}

/**
 * 글자로 담겼던 숫자를 숫자로 바꾼 뒤에도 보이던 대로 보이게 할 표시 형식을
 * 고른다. 고를 것이 없으면 undefined 다.
 *
 * 이것이 없으면 바꾸는 순간 화면이 달라진다. "50%" 가 "0.5" 가 되고
 * "₩5,000" 이 "5000" 이 된다. 값은 맞지만 사람은 자기 자료가 망가졌다고
 * 읽는다 — 특히 백분율은 수가 100분의 1로 줄어든 것처럼 보인다.
 *
 * 쓸 수 있는 것은 formatCellValue 가 그릴 줄 아는 것뿐이다. "1,234원" 의
 * "원" 같은 뒤에 붙는 단위는 서식 말에 없으므로 자릿점만 남기고 놓아준다.
 * 그리지 못할 서식을 적어 두면 칸이 빈 것처럼 보인다.
 */
export function numberFormatForText(value:string):string|undefined{
  if(textToNumber(value)===undefined)return undefined
  let text=value.trim()
  const parenthesized=/^\(.*\)$/.test(text)
  if(parenthesized)text=text.slice(1,-1).trim()
  const percent=text.endsWith('%')
  if(percent)text=text.slice(0,-1).trim()
  const currency=text.match(/^([₩$€¥£])/)?.[1]??''
  // 뒤에 붙은 단위도 서식으로 옮긴다. 예전에는 서식 말이 이것을 그리지 못해
  // 놓아주었는데, 이제 따옴표로 묶은 글자를 그대로 그린다.
  const unit=text.match(/\s*(원|달러|USD|KRW|won)$/i)?.[1]??''
  text=text.replace(/^[₩$€¥£]\s*/,'').replace(/\s*(원|달러|USD|KRW|won)$/i,'').trim()
  if(text.startsWith('-')||text.startsWith('+'))text=text.slice(1).trim()
  const grouped=text.includes(',')
  const decimals=text.split('.')[1]?.length??0
  if(!currency&&!unit&&!percent&&!grouped&&!parenthesized)return undefined
  // 소수 자리를 서식으로 굳히면 그 자리에서 반올림해 그린다. 사람이 적은
  // 자리보다 길게 굳힐 이유는 없다.
  // 기호는 따옴표로 묶는다. 엑셀 서식 말에서 따옴표 안은 언제나 그대로
  // 적는 글자이므로, $ 처럼 다른 뜻으로 읽힐 수 있는 기호도 안전하다.
  // 붙여넣기가 내는 서식과 같은 글자이기도 하다.
  const body=(currency?`"${currency}"`:'')+(grouped?'#,##0':'0')+(decimals?'.'+'0'.repeat(decimals):'')+(percent?'%':'')+(unit?`"${unit}"`:'')
  return parenthesized?`${body};(${body})`:body
}

/** 앞의 0과 소수점을 뺀 자릿수. 배정밀도가 정확히 담는 한도를 재는 데 쓴다. */
export function significantDigits(text:string){
  return text.replace('.','').replace(/^0+/,'').length
}
