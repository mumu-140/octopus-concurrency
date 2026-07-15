package model

import (
	"fmt"
	"net/url"
	"reflect"
)

// Clone 返回 InternalLLMRequest 的完整深拷贝。
//
// 用途（协议路由阶段 A）：每个协议 Attempt 必须使用独立的请求快照，
// 第一次转换不能修改回退尝试使用的内部请求（设计 §5.2 / §7.2）。
//
// 实现基于反射逐字段深拷贝：类型图中全部字段均为导出字段，且不含
// func/chan/unsafe 指针，因此反射拷贝对含 json:"-" 的字段同样完整；
// 相比 JSON round-trip 不会丢失任何序列化排除字段。
// url.Values 与 map/slice/pointer 会被逐层复制；json.RawMessage 复制底层字节。
func (r *InternalLLMRequest) Clone() *InternalLLMRequest {
	if r == nil {
		return nil
	}
	cloned := deepCopyValue(reflect.ValueOf(*r)).Interface().(InternalLLMRequest)
	return &cloned
}

// deepCopyValue 递归复制 v。仅支持请求类型图实际使用的 kind；
// 遇到不可复制的 kind 直接 panic，避免静默共享可变状态。
func deepCopyValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Invalid:
		return v
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		return v
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(deepCopyValue(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(deepCopyValue(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(deepCopyValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(deepCopyValue(iter.Key()), deepCopyValue(iter.Value()))
		}
		return out
	case reflect.Struct:
		// url.Values 是 map 别名结构外的唯一特殊类型，走通用结构复制即可
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				panic(fmt.Sprintf(
					"model.Clone: unexported field %s.%s cannot be deep-copied",
					v.Type(), v.Type().Field(i).Name))
			}
			out.Field(i).Set(deepCopyValue(v.Field(i)))
		}
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		inner := deepCopyValue(v.Elem())
		out := reflect.New(v.Type()).Elem()
		out.Set(inner)
		return out
	default:
		panic(fmt.Sprintf("model.Clone: unsupported kind %s (%s)", v.Kind(), v.Type()))
	}
}

// cloneURLValues 保留给需要单独复制 Query 的调用方。
func cloneURLValues(q url.Values) url.Values {
	if q == nil {
		return nil
	}
	out := make(url.Values, len(q))
	for k, vs := range q {
		out[k] = append([]string(nil), vs...)
	}
	return out
}
