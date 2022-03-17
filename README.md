Simple app that archives Github audit logs to AWS s3

## Usage

```hcl
module "github_audit_log_to_s3" {
  source = "git::ssh://git@github.com/pasali/github-audit-log-to-s3.git?ref=master"
  
  github_org    = "my_org"
  github_token  = "ghp_xxxxxxxxxxxxxxxxxxx"
  bucket_name   = "my-log-bucket"
  folder_prefix = "Github/Audit"
}
```