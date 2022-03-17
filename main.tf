module "lambda_main" {
  source  = "terraform-aws-modules/lambda/aws"
  version = "v2.34.1"

  function_name = "github-audit-log-to-s3"
  handler       = "main"
  runtime       = "go1.x"
  timeout       = 600
  publish       = true

  source_path = [
    {
      path     = "${path.module}/function/handler/"
      commands = [
        "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w' -o ${abspath(path.module)}/bin/handler/main main.go",
        ":zip ${abspath(path.module)}/bin/handler/main",
      ]
    }
  ]

  allowed_triggers = {
    ScanAmiRule = {
      principal  = "events.amazonaws.com"
      source_arn = aws_cloudwatch_event_rule.this.arn
    }
  }

  environment_variables = {
    "GITHUB_TOKEN"              = var.github_token
    "GITHUB_ORG"                = var.github_org
    "BUCKET_NAME"               = var.bucket_name
    "FOLDER_PREFIX"             = var.folder_prefix
    "AUDIT_LOG_OPTION_INCLUDE"  = var.audit_log_option_include
    "AUDIT_LOG_OPTION_ORDER"    = var.audit_log_option_order
    "AUDIT_LOG_OPTION_PER_PAGE" = var.audit_log_option_per_page
    "AUDIT_LOG_OPTION_PRHASE"   = var.audit_log_option_prhase
    "TIME_ZONE"   = var.time_zone
    "BOOKMARK_TABLE"            = aws_dynamodb_table.this.id
  }

  attach_policy_statements = true
  policy_statements        = {
    s3 = {
      effect    = "Allow",
      actions   = ["s3:PutObject"],
      resources = ["arn:aws:s3:::${var.bucket_name}/*"]
    }

    dynamodb = {
      effect    = "Allow",
      actions   = ["dynamodb:Query", "dynamodb:PutItem"],
      resources = [aws_dynamodb_table.this.arn]
    }
  }

  tags = {
    Name = "github-audit-log-to-s3"
  }
}


resource "aws_cloudwatch_event_rule" "this" {
  name                = "GithubAuditLogToS3Schedule"
  description         = "Fires once a every hour"
  schedule_expression = "rate(1 hour)"
}

resource "aws_cloudwatch_event_target" "this" {
  rule      = aws_cloudwatch_event_rule.this.name
  target_id = "lambda"
  arn       = module.lambda_main.lambda_function_arn
}

resource "aws_dynamodb_table" "this" {
  name         = "github-audit-log-to-s3"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "EventDate"
  range_key    = "CreatedAt"

  attribute {
    name = "EventDate"
    type = "S"
  }

  attribute {
    name = "CreatedAt"
    type = "S"
  }

  tags = {
    Name = "github-audit-log-to-s3"
  }
}
