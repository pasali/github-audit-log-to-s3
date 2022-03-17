variable "github_token" {
  type = string
}

variable "github_org" {
  type = string
}

variable "bucket_name" {
  type = string
}

variable "folder_prefix" {
  type    = string
  default = "Github/Audit"
}

variable "audit_log_option_include" {
  type    = string
  default = "web"
}

variable "audit_log_option_order" {
  type    = string
  default = "desc"
}

variable "audit_log_option_per_page" {
  type    = number
  default = 30
}

variable "audit_log_option_prhase" {
  type    = string
  default = ""
}

variable "time_zone" {
  type = string
  default = "UTC"
}
