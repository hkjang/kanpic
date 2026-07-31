import { parseFilterInput,parseFilterRange } from './filter'
import type { DataValidation,ValidationOperator,ValidationOption,ValidationViolation } from '../types'

export type ValidationInputCell={row:number;column:number;value?:unknown;formula?:string}

export function validationForCell(rules:DataValidation[],row:number,column:number){
  return rules.find(rule=>{const range=parseFilterRange(rule.range);return Boolean(range&&row>=range.startRow&&row<=range.endRow&&column>=range.startColumn&&column<=range.endColumn)})
}

export function optionForValue(rule:DataValidation,value:unknown){return rule.options?.find(option=>sameValue(option.value,value))}
export function optionLabel(option:ValidationOption){return option.label?.trim()||String(option.value)}
export function validationDraftValue(value:unknown){return value==null?'':String(value)}
export function validationOptionInput(value:string,label:string,color:string):ValidationOption{return{value:parseFilterInput(value),...(label.trim()?{label:label.trim()}:{}),...(color?{color}: {})}}

export function validateClientValue(rule:DataValidation,value:unknown):{valid:boolean;deferred:boolean;message:string}{
  if(value==null||value==='')return{valid:rule.allow_blank,deferred:false,message:rule.help_text||'빈 값은 허용되지 않습니다.'}
  let valid=false,deferred=false
  if(rule.rule_type==='list')valid=Boolean(optionForValue(rule,value))
  else if(rule.rule_type==='number')valid=typeof value==='number'&&Number.isFinite(value)&&compare(value,Number(rule.value),Number(rule.value2),rule.operator)
  else if(rule.rule_type==='date'){const actual=dateValue(value),first=dateValue(rule.value),second=dateValue(rule.value2);valid=actual!==undefined&&first!==undefined&&compare(actual,first,second??0,rule.operator)}
  else{valid=true;deferred=true}
  const fallback=rule.rule_type==='list'?'목록에 있는 값을 선택해야 합니다.':rule.rule_type==='number'?'숫자 검증 조건을 만족하지 않습니다.':rule.rule_type==='date'?'날짜 검증 조건을 만족하지 않습니다.':'사용자 지정 수식은 서버에서 검사됩니다.'
  return{valid,deferred,message:rule.help_text||fallback}
}

export function validateClientInputs(rules:DataValidation[],inputs:ValidationInputCell[]){
  const rejected:ValidationViolation[]=[],warnings:ValidationViolation[]=[]
  for(const input of inputs){if(input.formula)continue;const rule=validationForCell(rules,input.row,input.column);if(!rule)continue;const result=validateClientValue(rule,input.value);if(result.valid||result.deferred)continue;const violation={validation_id:rule.id,row:input.row,column:input.column,message:result.message};(rule.reject_input?rejected:warnings).push(violation)}
  return{rejected,warnings}
}

export function comparisonNeedsSecond(operator:ValidationOperator){return operator==='between'||operator==='not_between'}

function sameValue(left:unknown,right:unknown){return typeof left===typeof right&&Object.is(left,right)}
function compare(actual:number,first:number,second:number,operator:ValidationOperator){if(!Number.isFinite(first))return false;switch(operator){case'between':return Number.isFinite(second)&&actual>=first&&actual<=second;case'not_between':return Number.isFinite(second)&&(actual<first||actual>second);case'equal':return actual===first;case'not_equal':return actual!==first;case'greater_than':return actual>first;case'greater_or_equal':return actual>=first;case'less_than':return actual<first;case'less_or_equal':return actual<=first;default:return false}}
function dateValue(value:unknown){if(typeof value==='number'&&Number.isFinite(value))return Date.UTC(1899,11,30)+value*86_400_000;if(typeof value!=='string')return;const timestamp=Date.parse(value);return Number.isFinite(timestamp)?timestamp:undefined}
